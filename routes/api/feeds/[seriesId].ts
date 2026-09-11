import { STATUS_CODE } from "@std/http/status";
import { define } from "../../../utils.ts";
import { caching } from "../../../lib/caching.ts";
import { rss } from "../../../lib/rss.ts";
import {
  etagFor,
  etagMatches,
  getOrigin,
  isValidResourceId,
  responseJSON,
  responseXML,
  withExpiry,
} from "../../../lib/utils.ts";

const FEED_TTL_SECONDS = 2 * 60 * 60;

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

    const feed = rss.assembleFeed(series, getOrigin(ctx.req));
    const etag = await etagFor(feed);

    // podcast clients poll constantly; answer unchanged feeds with 304
    if (etagMatches(ctx.req.headers.get("if-none-match"), etag)) {
      return withExpiry(
        new Response(null, { status: STATUS_CODE.NotModified, headers: { ETag: etag } }),
        FEED_TTL_SECONDS,
      );
    }

    const response = responseXML(feed, STATUS_CODE.OK);
    response.headers.set("ETag", etag);
    return withExpiry(response, FEED_TTL_SECONDS);
  },
});
