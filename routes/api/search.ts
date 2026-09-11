import { STATUS_CODE } from "@std/http/status";
import { define } from "../../utils.ts";
import { nrkRadio } from "../../lib/nrk/nrk.ts";
import { toSeriesSummary } from "../../lib/series-summary.ts";
import { responseJSON, withCacheHeaders } from "../../lib/utils.ts";

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

    const result = await nrkRadio.search(query);
    const results = (result ?? []).map(toSeriesSummary);

    return withCacheHeaders(
      responseJSON({ results }, STATUS_CODE.OK),
      { maxAge: SEARCH_TTL_SECONDS, sMaxAge: SEARCH_TTL_SECONDS },
    );
  },
});
