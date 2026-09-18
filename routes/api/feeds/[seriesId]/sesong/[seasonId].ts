import { STATUS_CODE } from "@std/http/status";
import { define } from "../../../../../utils.ts";
import { seasonFeedId } from "../../../../../lib/feed-id.ts";
import { serveFeed } from "../../../../../lib/feed-handler.ts";
import { isValidResourceId, responseJSON } from "../../../../../lib/utils.ts";

/**
 * Feed for one customSeason of an umbrella podcast. NRK publishes
 * standalone shows this way (own title, artwork and description under
 * radio.nrk.no/podkast/{seriesId}/sesong/{seasonId}), so each gets its
 * own feed, mirroring NRK's structure.
 */
export const handler = define.handlers({
  GET(ctx) {
    const { seriesId, seasonId } = ctx.params;
    if (!isValidResourceId(seriesId) || !isValidResourceId(seasonId)) {
      return responseJSON({ message: "Invalid feed id" }, STATUS_CODE.BadRequest);
    }
    return serveFeed(ctx.req, seasonFeedId(seriesId, seasonId));
  },
});
