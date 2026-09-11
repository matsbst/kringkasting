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
