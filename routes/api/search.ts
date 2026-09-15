import { STATUS_CODE } from "@std/http/status";
import { define } from "../../utils.ts";
import { nrkRadio } from "../../lib/nrk/nrk.ts";
import { toSummaries } from "../../lib/series-summary.ts";
import { responseJSON, withCacheHeaders } from "../../lib/utils.ts";
import { allowScoped, getClientKey } from "../../lib/rate-limit.ts";
import { recordSearchServed } from "../../lib/stats.ts";
import { storage } from "../../lib/storage.ts";

const MIN_QUERY_LENGTH = 2;
const MAX_QUERY_LENGTH = 100;

/** matches the server-side search micro-cache TTL */
const SEARCH_TTL_SECONDS = 5 * 60;

export const handler = define.handlers({
  async GET(ctx) {
    const query = new URL(ctx.req.url).searchParams.get("q")?.trim() ?? "";
    if (query.length < MIN_QUERY_LENGTH || query.length > MAX_QUERY_LENGTH) {
      return responseJSON({ results: [] }, STATUS_CODE.OK);
    }

    if (!allowScoped("search", getClientKey(ctx.req), 30, 0.5, 120, 2)) {
      const response = responseJSON({ message: "Too many requests" }, STATUS_CODE.TooManyRequests);
      response.headers.set("Retry-After", "30");
      response.headers.set("Cache-Control", "no-store");
      return response;
    }

    recordSearchServed();
    const result = await nrkRadio.search(query);
    if (result === null || result === undefined) {
      // upstream failure: never cache it as an empty result
      const response = responseJSON({ message: "Search unavailable" }, STATUS_CODE.ServiceUnavailable);
      response.headers.set("Cache-Control", "no-store");
      return response;
    }

    const streamOnly = storage.streamOnlySeriesIds(result.map((series) => series.seriesId));
    return withCacheHeaders(
      responseJSON({ results: toSummaries(result, streamOnly) }, STATUS_CODE.OK),
      { maxAge: SEARCH_TTL_SECONDS, sMaxAge: SEARCH_TTL_SECONDS },
    );
  },
});
