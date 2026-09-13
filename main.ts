import { App, HttpError, staticFiles } from "fresh";
import { type State } from "./utils.ts";
import { captureException, errorReportingEnabled } from "./lib/errors.ts";

export const app = new App<State>();

// background failures (backlog crawler, unawaited work) surface here
if (errorReportingEnabled()) {
  globalThis.addEventListener("unhandledrejection", (event) => {
    captureException(event.reason, { tags: { kind: "unhandledrejection" } });
  });
  globalThis.addEventListener("error", (event) => {
    captureException(event.error ?? event.message, { tags: { kind: "uncaught" } });
  });
}

const SECURITY_HEADERS: Record<string, string> = {
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "same-origin",
  "X-Frame-Options": "DENY",
};

// report handler exceptions, then rethrow so Fresh still renders _error.
// Only the path/method are attached — never query strings or client IP.
app.use(async (ctx) => {
  try {
    return await ctx.next();
  } catch (error) {
    if (!(error instanceof HttpError)) {
      captureException(error, {
        tags: { method: ctx.req.method, path: new URL(ctx.req.url).pathname },
      });
    }
    throw error;
  }
});

app.use(async (ctx) => {
  const response = await ctx.next();
  // responses may have immutable headers (e.g. from fetch), so rebuild
  const headers = new Headers(response.headers);
  for (const [name, value] of Object.entries(SECURITY_HEADERS)) {
    headers.set(name, value);
  }
  // fonts are content-stable; let browsers and the CDN keep them forever
  if (new URL(ctx.req.url).pathname.startsWith("/fonts/")) {
    headers.set("Cache-Control", "public, max-age=31536000, immutable");
  }
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
});

app.use(staticFiles());
app.fsRoutes();
