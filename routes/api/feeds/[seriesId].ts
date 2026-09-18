import { STATUS_CODE } from "@std/http/status";
import { define } from "../../../utils.ts";
import { serveFeed } from "../../../lib/feed-handler.ts";
import { isValidResourceId, responseJSON } from "../../../lib/utils.ts";

export const handler = define.handlers({
  GET(ctx) {
    const seriesId = ctx.params.seriesId;
    if (!isValidResourceId(seriesId)) {
      return responseJSON({ message: "Invalid series id" }, STATUS_CODE.BadRequest);
    }
    return serveFeed(ctx.req, seriesId);
  },
});
