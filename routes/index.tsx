import { page } from "fresh";
import { Head } from "fresh/runtime";
import { define } from "../utils.ts";
import InstantSearch from "../islands/InstantSearch.tsx";

import { nrkRadio } from "../lib/nrk/nrk.ts";
import { getRandomShowTitles } from "../lib/catalog.ts";
import { SeriesSummary, toSeriesSummary } from "../lib/series-summary.ts";
import { getOrigin } from "../lib/utils.ts";

/** shown until the live catalog has loaded */
const FALLBACK_SUGGESTIONS = [
  "Oppdatert",
  "Hele historien",
  "Radioresepsjonen",
  "Abels tårn",
  "Trygdekontoret",
  "Berrum & Beyer",
];

export const handler = define.handlers({
  async GET(ctx) {
    const query = new URL(ctx.req.url).searchParams.get("query");
    let results: SeriesSummary[] | null = null;
    if (query && query.trim().length >= 2) {
      const searchResult = await nrkRadio.search(query);
      results = (searchResult ?? []).map(toSeriesSummary);
    }
    const suggestions = getRandomShowTitles(6) ?? FALLBACK_SUGGESTIONS;
    return page({ query, results, suggestions, origin: getOrigin(ctx.req) });
  },
});

export default define.page<typeof handler>(function Home({ data }) {
  return (
    <>
      <Head>
        {data.query && <title>Søk: {data.query} – Kringkasting</title>}
        {/* search-result URLs are duplicates of the front page for crawlers */}
        {data.query && <meta name="robots" content="noindex,follow" />}
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{
            __html: JSON.stringify({
              "@context": "https://schema.org",
              "@type": "WebSite",
              name: "Kringkasting",
              url: data.origin,
              description:
                "Åpne RSS-strømmer for NRK sine podkaster, med komplette episodearkiv – hør dem i den podkast-appen du selv vil.",
              inLanguage: "nb",
              potentialAction: {
                "@type": "SearchAction",
                target: {
                  "@type": "EntryPoint",
                  urlTemplate: `${data.origin}/?query={search_term_string}`,
                },
                "query-input": "required name=search_term_string",
              },
            }),
          }}
        />
      </Head>

      <section class="relative pt-20 pb-12">
        <h1 class="relative text-5xl sm:text-6xl font-bold tracking-tight leading-[1.02] text-balance">
          NRK-podkaster, i appen <span class="text-brand">du</span> velger
        </h1>
        <p class="relative mt-5 text-lg text-ink-2 dark:text-ink-2-dark max-w-lg text-pretty">
          NRK låser podkastene sine inne i sin egen app. Det bør ikke en skattefinansiert rikskringkaster gjøre.
          Kringkasting åpner podkastene opp igjen som helt vanlige RSS-strømmer slik at du kan bruke den i appen du
          velger.
        </p>
      </section>

      <InstantSearch
        initialQuery={data.query}
        initialResults={data.results}
        suggestions={data.suggestions}
        origin={data.origin}
      />
    </>
  );
});
