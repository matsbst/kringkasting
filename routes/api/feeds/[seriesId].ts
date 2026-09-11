import { STATUS_CODE } from "@std/http/status";
import { define } from "../../../utils.ts";
import { caching } from "../../../lib/caching.ts";
import { storage } from "../../../lib/storage.ts";
import { allowRequest, getClientKey } from "../../../lib/rate-limit.ts";
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

    // fetching an unknown series costs real upstream work; cap the rate
    // per client (12 cold fetches/min, burst 10). Known series are cheap
    // and never limited.
    if (!storage.hasSeries(seriesId) && !allowRequest(`coldfeed:${getClientKey(ctx.req)}`, 10, 0.2)) {
      const response = responseJSON({ message: "Too many requests" }, STATUS_CODE.TooManyRequests);
      response.headers.set("Retry-After", "60");
      response.headers.set("Cache-Control", "no-store");
      return response;
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
