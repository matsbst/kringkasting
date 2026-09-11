import { STATUS_CODE } from "@std/http/status";
import { define } from "../../../utils.ts";
import { caching } from "../../../lib/caching.ts";
import { renderFeed } from "../../../lib/feed-cache.ts";
import {
  etagMatches,
  getOrigin,
  isNotModifiedSince,
  isValidResourceId,
  responseJSON,
  responseXML,
  withCacheHeaders,
} from "../../../lib/utils.ts";

/** what podcast clients may cache */
const FEED_MAX_AGE_SECONDS = 30 * 60;
/** what shared caches (Cloudflare) may cache; ~matches the hourly NRK sync */
const FEED_S_MAXAGE_SECONDS = 60 * 60;

export const handler = define.handlers({
  async GET(ctx) {
    const seriesId = ctx.params.seriesId;
    if (!isValidResourceId(seriesId)) {
      return responseJSON({ message: "Invalid series id" }, STATUS_CODE.BadRequest);
    }

    let series;
    try {
      series = await caching.getSeries({ id: seriesId });
    } catch (error) {
      console.error(`Failed to get series ${seriesId}: ${error}`);
      return responseJSON({ message: "Upstream error" }, STATUS_CODE.BadGateway);
    }

    if (!series) {
      return responseJSON({ message: "Series not found" }, STATUS_CODE.NotFound);
    }

    const feed = await renderFeed(series, getOrigin(ctx.req));

    // podcast clients poll constantly; answer unchanged feeds with 304.
    // If-None-Match takes precedence over If-Modified-Since (RFC 9110).
    const ifNoneMatch = ctx.req.headers.get("if-none-match");
    const notModified = ifNoneMatch
      ? etagMatches(ifNoneMatch, feed.etag)
      : isNotModifiedSince(ctx.req.headers.get("if-modified-since"), feed.lastModified);

    const response = notModified
      ? new Response(null, { status: STATUS_CODE.NotModified })
      : responseXML(feed.xml, STATUS_CODE.OK);

    response.headers.set("ETag", feed.etag);
    response.headers.set("Last-Modified", feed.lastModified.toUTCString());
    return withCacheHeaders(response, {
      maxAge: FEED_MAX_AGE_SECONDS,
      sMaxAge: FEED_S_MAXAGE_SECONDS,
    });
  },
});
