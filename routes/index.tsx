import { page } from "fresh";
import { Head } from "fresh/runtime";
import { define } from "../utils.ts";
import InstantSearch from "../islands/InstantSearch.tsx";
import { nrkRadio } from "../lib/nrk/nrk.ts";
import { SeriesSummary, toSeriesSummary } from "../lib/series-summary.ts";
import { getOrigin } from "../lib/utils.ts";

export const handler = define.handlers({
  async GET(ctx) {
    const query = new URL(ctx.req.url).searchParams.get("query");
    let results: SeriesSummary[] | null = null;
    if (query && query.trim().length >= 2) {
      const searchResult = await nrkRadio.search(query);
      results = (searchResult ?? []).map(toSeriesSummary);
    }
    return page({ query, results, origin: getOrigin(ctx.req) });
  },
});

export default define.page<typeof handler>(function Home({ data }) {
  return (
    <>
      {data.query && (
        <Head>
          <title>Søk: {data.query} – Kringkasting</title>
        </Head>
      )}

      <section class="pt-16 pb-10">
        <h1 class="text-4xl sm:text-[2.75rem] font-semibold tracking-tight leading-[1.1] text-balance">
          NRK-podkaster, i appen du velger
        </h1>
        <p class="mt-4 text-lg text-ink-2 dark:text-ink-2-dark max-w-prose">
          NRK låser podkastene sine inne i sin egen app. Kringkasting åpner dem opp igjen som helt vanlige RSS-strømmer.
        </p>
      </section>

      <InstantSearch initialQuery={data.query} initialResults={data.results} origin={data.origin} />
    </>
  );
});
