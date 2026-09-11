import { STATUS_CODE } from "@std/http/status";
import { define } from "../../../utils.ts";
import { caching } from "../../../lib/caching.ts";
import { rss } from "../../../lib/rss.ts";
import { getOrigin, responseJSON, responseXML, withExpiry } from "../../../lib/utils.ts";

export const handler = define.handlers({
  async GET(ctx) {
    const seriesId = ctx.params.seriesId;

    const series = await caching.getSeries({ id: seriesId });
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
