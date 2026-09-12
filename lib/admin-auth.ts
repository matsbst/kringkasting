/**
 * Cookie auth for /admin, driven by the ADMIN_TOKEN env var (set it in
 * Cloudron's env settings). No token configured = admin disabled.
 */

const COOKIE_NAME = "kringkasting_admin";

async function sha256Hex(value: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return Array.from(new Uint8Array(digest)).map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

function configuredToken(): string | null {
  const token = Deno.env.get("ADMIN_TOKEN");
  return token && token.length >= 8 ? token : null;
}

export function adminEnabled(): boolean {
  return configuredToken() !== null;
}

/** hash comparison instead of direct string compare: constant-time enough */
async function matches(candidate: string): Promise<boolean> {
  const token = configuredToken();
  if (!token) {
    return false;
  }
  return (await sha256Hex(candidate)) === (await sha256Hex(token));
}

function readCookie(request: Request): string | null {
  const header = request.headers.get("cookie") ?? "";
  for (const part of header.split(";")) {
    const [name, ...rest] = part.trim().split("=");
    if (name === COOKIE_NAME) {
      return decodeURIComponent(rest.join("="));
    }
  }
  return null;
}

export async function isAuthenticated(request: Request): Promise<boolean> {
  const cookie = readCookie(request);
  return cookie !== null && await matches(cookie);
}

/**
 * Behind Cloudron/Cloudflare the upstream request is plain HTTP, so the
 * request URL alone would drop the Secure attribute on an HTTPS site.
 * Trust the forwarded proto / configured origin; only genuinely local
 * HTTP development skips Secure.
 */
function isHttps(request: Request): boolean {
  const forwardedProto = request.headers.get("x-forwarded-proto");
  if (forwardedProto) {
    return forwardedProto.split(",")[0].trim() === "https";
  }
  const configured = Deno.env.get("APP_ORIGIN") ?? Deno.env.get("CLOUDRON_APP_ORIGIN");
  if (configured) {
    return configured.startsWith("https:");
  }
  return new URL(request.url).protocol === "https:";
}

export async function tryLogin(request: Request, submittedToken: string): Promise<Headers | null> {
  if (!(await matches(submittedToken))) {
    return null;
  }
  const secure = isHttps(request) ? " Secure;" : "";
  const headers = new Headers();
  headers.set(
    "Set-Cookie",
    `${COOKIE_NAME}=${
      encodeURIComponent(submittedToken)
    }; Path=/admin; HttpOnly;${secure} SameSite=Strict; Max-Age=2592000`,
  );
  return headers;
}
