import { STATUS_CODE } from "@std/http/status";

type EnumValues<T> = T[keyof T];
type Status = EnumValues<typeof STATUS_CODE>;

/**
 * The public origin of this instance, used for absolute URLs in feeds.
 *
 * Prefers an explicit APP_ORIGIN, then Cloudron's CLOUDRON_APP_ORIGIN,
 * and falls back to the origin of the incoming request (which may be the
 * internal address when running behind a reverse proxy).
 */
export function getOrigin(request: Request): string {
  const configured = Deno.env.get("APP_ORIGIN") ?? Deno.env.get("CLOUDRON_APP_ORIGIN");
  if (configured) {
    return configured.replace(/\/$/, "");
  }
  return new URL(request.url).origin;
}

/**
 * NRK series/episode ids are short slugs. Rejecting anything else keeps
 * user-controlled route params from steering requests to other paths on
 * NRK's API (or growing the KV keyspace with garbage).
 */
const RESOURCE_ID_PATTERN = /^[a-zA-Z0-9_-]{1,100}$/;

export function isValidResourceId(id: string): boolean {
  return RESOURCE_ID_PATTERN.test(id);
}

/** strong ETag (quoted SHA-1 hex) for a response body */
export async function etagFor(content: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-1", new TextEncoder().encode(content));
  const hex = Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, "0"))
    .join("");
  return `"${hex}"`;
}

/** does an If-None-Match header value match the given ETag? */
export function etagMatches(ifNoneMatch: string | null, etag: string): boolean {
  if (!ifNoneMatch) {
    return false;
  }
  return ifNoneMatch
    .split(",")
    .map((candidate) => candidate.trim().replace(/^W\//, ""))
    .some((candidate) => candidate === etag || candidate === "*");
}

export function responseJSON(body: unknown | null, status: Status) {
  const stringifiedBody = JSON.stringify(body);
  return response(stringifiedBody, status, "json");
}

export function responseXML(body: string, status: Status) {
  return response(body, status, "xml");
}

export const withExpiry = (response: Response, ttlInSeconds: number) => {
  const clonedResponse = response.clone();
  clonedResponse.headers.set("Cache-Control", `max-age=${ttlInSeconds}`);
  clonedResponse.headers.set("Expires", new Date(Date.now() + ttlInSeconds * 1000).toUTCString());
  return clonedResponse;
};

function response(body: string, status: number, type: "json" | "xml") {
  return new Response(body, {
    status,
    headers: {
      "Content-Type": `application/${type}`,
    },
  });
}
