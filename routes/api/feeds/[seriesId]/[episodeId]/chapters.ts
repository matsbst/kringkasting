import { STATUS_CODE } from "@std/http/status";
import { parse, toSeconds } from "iso8601-duration";
import { define } from "../../../../../utils.ts";
import { NrkPodcastEpisode, nrkRadio } from "../../../../../lib/nrk/nrk.ts";
import { isValidResourceId, responseJSON } from "../../../../../lib/utils.ts";
import { allowScoped, getClientKey } from "../../../../../lib/rate-limit.ts";

type Chapter = {
  title: string | undefined;
  startTime: number | undefined;
};

function toChapters(episode: NrkPodcastEpisode): Chapter[] | null {
  if (!episode.indexPoints) {
    return null;
  }
  return episode.indexPoints.map((indexPoint) => ({
    title: indexPoint.title,
    startTime: indexPoint.startPoint ? toSeconds(parse(indexPoint.startPoint)) : undefined,
  }));
}

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

    const episode = await nrkRadio.getEpisode(seriesId, episodeId);

    if (!episode) {
      return responseJSON({ message: `Episode ${episodeId} is missing` }, STATUS_CODE.NotFound);
    }

    const chapters = toChapters(episode);
    const body = {
      version: "1.2.0",
      chapters,
    };

    return responseJSON(body, STATUS_CODE.OK);
  },
});
