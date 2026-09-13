/**
 * Minimal Sentry-protocol error reporter, compatible with Bugsink.
 * Hand-rolled (like xml.ts / http.ts) to avoid a large SDK dependency
 * and to keep full control over what leaves the process: no IP, no
 * request bodies, no query strings — only the exception and a couple of
 * non-identifying tags.
 *
 * Enabled by the SENTRY_DSN env var; a no-op when unset.
 */

const REPORT_TIMEOUT_MS = 5_000;

type Dsn = {
  endpoint: string;
  publicKey: string;
};

function parseDsn(raw: string): Dsn | null {
  try {
    // https://<publicKey>@<host>[/<pathPrefix>]/<projectId>
    const url = new URL(raw);
    const segments = url.pathname.split("/").filter(Boolean);
    const projectId = segments.pop();
    if (!url.username || !projectId) {
      return null;
    }
    // preserve any reverse-proxy subpath in front of the project id
    const prefix = segments.length ? `/${segments.join("/")}` : "";
    return {
      endpoint: `${url.protocol}//${url.host}${prefix}/api/${projectId}/store/`,
      publicKey: url.username,
    };
  } catch {
    return null;
  }
}

/** strip query strings and fragments from any URLs in reported text */
function scrub(text: string): string {
  return text.replace(/(https?:\/\/[^\s?#]+)[?#]\S*/g, "$1").slice(0, 2000);
}

let dsn: Dsn | null | undefined;

function getDsn(): Dsn | null {
  if (dsn === undefined) {
    const raw = Deno.env.get("SENTRY_DSN");
    dsn = raw ? parseDsn(raw) : null;
    if (raw && !dsn) {
      console.error("SENTRY_DSN is set but could not be parsed; error reporting disabled");
    }
  }
  return dsn;
}

export function errorReportingEnabled(): boolean {
  return getDsn() !== null;
}

type Context = { tags?: Record<string, string>; extra?: Record<string, unknown> };

/**
 * Report an exception, fire-and-forget. Never throws and never blocks the
 * caller's failure path — reporting problems must not become app problems.
 */
export function captureException(error: unknown, context: Context = {}): void {
  const target = getDsn();
  if (!target) {
    return;
  }
  send(target, error, context).catch((reportingError) => {
    console.error(`Error reporting failed: ${reportingError}`);
  });
}

async function send(target: Dsn, error: unknown, context: Context): Promise<void> {
  const err = error instanceof Error ? error : new Error(String(error));
  const event = {
    event_id: crypto.randomUUID().replaceAll("-", ""),
    timestamp: new Date().toISOString(),
    platform: "javascript",
    level: "error",
    logger: "kringkasting",
    release: Deno.env.get("DENO_DEPLOYMENT_ID") ?? "dev",
    environment: Deno.env.get("APP_ORIGIN") || Deno.env.get("CLOUDRON_APP_ORIGIN") ? "production" : "development",
    exception: {
      values: [{
        type: err.name,
        // scrub in case a message interpolates a URL with a query string
        value: scrub(err.message),
        stacktrace: err.stack ? { frames: parseStack(err.stack) } : undefined,
      }],
    },
    tags: context.tags,
    extra: context.extra,
  };

  await fetch(target.endpoint, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-sentry-auth": `Sentry sentry_version=7, sentry_client=kringkasting/1.0, sentry_key=${target.publicKey}`,
    },
    body: JSON.stringify(event),
    signal: AbortSignal.timeout(REPORT_TIMEOUT_MS),
  }).then((response) => response.body?.cancel());
}

/** Sentry wants oldest frame first; V8 stacks are newest first */
function parseStack(stack: string) {
  const frames = stack
    .split("\n")
    .slice(1)
    .map((line) => {
      const match = line.match(/at (?:async )?(?:(.+?) )?\(?(.+?):(\d+):(\d+)\)?$/);
      if (!match) {
        return null;
      }
      return {
        function: match[1] ?? "?",
        filename: scrub(match[2]),
        lineno: Number(match[3]),
        colno: Number(match[4]),
      };
    })
    .filter((frame): frame is NonNullable<typeof frame> => frame !== null);
  return frames.reverse();
}

export const forTestingOnly = { parseDsn, parseStack };
