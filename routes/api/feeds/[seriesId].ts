import { STATUS_CODE } from "@std/http/status";
import { define } from "../../../utils.ts";
import { caching } from "../../../lib/caching.ts";
import { rss } from "../../../lib/rss.ts";
import { getOrigin, isValidResourceId, responseJSON, responseXML, withExpiry } from "../../../lib/utils.ts";

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

    return withExpiry(
      responseXML(feed, STATUS_CODE.OK),
      // 2 hours
      2 * 60 * 60,
    );
  },
});
