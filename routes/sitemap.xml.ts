import { STATUS_CODE } from "@std/http/status";
import { define } from "../utils.ts";
import { getOrigin, responseXML, withCacheHeaders } from "../lib/utils.ts";

/** grows as more indexable pages arrive (podcast directory, series pages) */
const PATHS = ["/"];

export const handler = define.handlers({
  GET(ctx) {
    const origin = getOrigin(ctx.req);
    const urls = PATHS
      .map((path) => `<url><loc>${origin}${path}</loc></url>`)
      .join("");
    const xml = `<?xml version="1.0" encoding="UTF-8"?>` +
      `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${urls}</urlset>`;
    return withCacheHeaders(responseXML(xml, STATUS_CODE.OK), { maxAge: 3600, sMaxAge: 86400 });
  },
});
