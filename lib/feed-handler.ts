import { STATUS_CODE } from "@std/http/status";
import { caching } from "./caching.ts";
import { storage } from "./storage.ts";
import { allowScoped, getClientKey } from "./rate-limit.ts";
import { peekFeed, type RenderedFeed, renderFeed } from "./feed-cache.ts";
import { recordFeedServed } from "./stats.ts";
import { etagMatches, getOrigin, isNotModifiedSince, responseJSON, responseXML, withCacheHeaders } from "./utils.ts";

/** what podcast clients may cache */
const FEED_MAX_AGE_SECONDS = 30 * 60;
/** what shared caches (Cloudflare) may cache; ~matches the hourly NRK sync */
const FEED_S_MAXAGE_SECONDS = 60 * 60;

/**
 * Serve one feed — a whole series or a customSeason — identified by its
 * feed id (the storage key AND the /api/feeds/... path). The caller has
 * already validated the id.
 */
export async function serveFeed(request: Request, feedId: string): Promise<Response> {
  // fetching an unknown feed costs real upstream work; cap the rate
  // per client (12 cold fetches/min, burst 10). Known feeds are cheap
  // and never limited.
  if (!storage.hasSeries(feedId) && !allowScoped("coldfeed", getClientKey(request), 10, 0.2, 30, 0.5)) {
    const response = responseJSON({ message: "Too many requests" }, STATUS_CODE.TooManyRequests);
    response.headers.set("Retry-After", "60");
    response.headers.set("Cache-Control", "no-store");
    return response;
  }

  const origin = getOrigin(request);

  // fast path: a fresh feed whose rendered XML is memoized needs no
  // episode read, no serialization, no hashing — most polls land here
  const versionBeforeRead = storage.getDataVersion(feedId);
  const memoized = peekFeed(feedId, origin, versionBeforeRead);
  if (memoized && caching.metaIsFresh(feedId)) {
    return respond(request, memoized);
  }

  let series;
  try {
    series = await caching.getSeries({ id: feedId });
  } catch (error) {
    console.error(`Failed to get series ${feedId}: ${error}`);
    return responseJSON({ message: "Upstream error" }, STATUS_CODE.BadGateway);
  }

  if (!series) {
    return responseJSON({ message: "Series not found" }, STATUS_CODE.NotFound);
  }

  const feed = await renderFeed(series, origin, versionBeforeRead);
  return respond(request, feed);
}

function respond(request: Request, feed: RenderedFeed): Response {
  // podcast clients poll constantly; answer unchanged feeds with 304.
  // If-None-Match takes precedence over If-Modified-Since (RFC 9110).
  const ifNoneMatch = request.headers.get("if-none-match");
  const notModified = ifNoneMatch
    ? etagMatches(ifNoneMatch, feed.etag)
    : isNotModifiedSince(request.headers.get("if-modified-since"), feed.lastModified);

  recordFeedServed(notModified);
  const response = notModified
    ? new Response(null, { status: STATUS_CODE.NotModified })
    : responseXML(feed.xml, STATUS_CODE.OK);

  response.headers.set("ETag", feed.etag);
  response.headers.set("Last-Modified", feed.lastModified.toUTCString());
  return withCacheHeaders(response, {
    maxAge: FEED_MAX_AGE_SECONDS,
    sMaxAge: FEED_S_MAXAGE_SECONDS,
  });
}
