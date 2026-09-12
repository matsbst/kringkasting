import { STATUS_CODE } from "@std/http/status";
import { define } from "../../../../../utils.ts";
import { getChapters } from "../../../../../lib/chapters.ts";
import { isValidResourceId, responseJSON } from "../../../../../lib/utils.ts";
import { allowScoped, getClientKey } from "../../../../../lib/rate-limit.ts";

export const handler = define.handlers({
  async GET(ctx) {
    const seriesId = ctx.params.seriesId;
    const episodeId = ctx.params.episodeId;
    if (!isValidResourceId(seriesId) || !isValidResourceId(episodeId)) {
      return responseJSON({ message: "Invalid id" }, STATUS_CODE.BadRequest);
    }

    if (!allowScoped("chapters", getClientKey(ctx.req), 20, 0.5, 60, 1)) {
      const response = responseJSON({ message: "Too many requests" }, STATUS_CODE.TooManyRequests);
      response.headers.set("Retry-After", "30");
      response.headers.set("Cache-Control", "no-store");
      return response;
    }

    const result = await getChapters(seriesId, episodeId);
    if (result.status === 503) {
      const response = responseJSON({ message: "Chapters unavailable" }, STATUS_CODE.ServiceUnavailable);
      response.headers.set("Cache-Control", "no-store");
      return response;
    }
    const response = result.status === 404
      ? responseJSON({ message: `Episode ${episodeId} is missing` }, STATUS_CODE.NotFound)
      : responseJSON({ version: "1.2.0", chapters: result.chapters }, STATUS_CODE.OK);
    response.headers.set("Cache-Control", "public, max-age=86400, s-maxage=86400");
    return response;
  },
});
