import { App, staticFiles } from "fresh";
import { type State } from "./utils.ts";

export const app = new App<State>();

const SECURITY_HEADERS: Record<string, string> = {
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "same-origin",
  "X-Frame-Options": "DENY",
};

app.use(async (ctx) => {
  const response = await ctx.next();
  // responses may have immutable headers (e.g. from fetch), so rebuild
  const headers = new Headers(response.headers);
  for (const [name, value] of Object.entries(SECURITY_HEADERS)) {
    headers.set(name, value);
  }
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
});

app.use(staticFiles());
app.fsRoutes();
