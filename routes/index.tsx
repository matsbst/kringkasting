import { page } from "fresh";
import { Head } from "fresh/runtime";
import { define } from "../utils.ts";
import Search from "../components/Search.tsx";
import SeriesCard from "../components/SeriesCard.tsx";
import { nrkRadio, NrkSearchResultList } from "../lib/nrk/nrk.ts";
import { getOrigin } from "../lib/utils.ts";

const SUGGESTIONS = [
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
    const result = query ? await nrkRadio.search(query) : null;
    return page({ query, result, origin: getOrigin(ctx.req) });
  },
});

export default define.page<typeof handler>(function Home({ data }) {
  return (
    <>
      {data.query && (
        <Head>
          <title>Søk: {data.query} – NRSS</title>
        </Head>
      )}

      <section class="pt-10 pb-8 text-center">
        <h1 class="font-display text-4xl sm:text-5xl font-bold tracking-tight text-balance">
          NRK-podkaster, i appen <span class="text-accent dark:text-accent-dark">du</span> velger
        </h1>
        <p class="mt-4 text-lg text-ink-soft dark:text-ink-soft-dark max-w-xl mx-auto text-pretty">
          NRK låser podkastene sine inne i sin egen app. NRSS åpner dem opp igjen som helt vanlige RSS-strømmer.
        </p>
      </section>

      <Search defaultValue={data.query} />

      {data.query ? <SearchResults query={data.query} result={data.result} origin={data.origin} /> : <HowItWorks />}
    </>
  );
});

function SearchResults(
  props: { query: string; result: NrkSearchResultList | null; origin: string },
) {
  if (!props.result || props.result.length === 0) {
    return (
      <section class="mt-12 text-center space-y-2">
        <h2 class="font-display text-2xl font-bold">Ingen treff for «{props.query}»</h2>
        <p class="text-ink-soft dark:text-ink-soft-dark">
          Prøv et annet søkeord, eller sjekk skrivemåten.
        </p>
      </section>
    );
  }

  return (
    <section class="mt-10">
      <h2 class="sr-only">Søkeresultat</h2>
      <p class="text-sm text-ink-soft dark:text-ink-soft-dark mb-4">
        {props.result.length} treff for «{props.query}»
      </p>
      <div class="space-y-4">
        {props.result.map((serie) => <SeriesCard key={serie.seriesId} serie={serie} origin={props.origin} />)}
      </div>
    </section>
  );
}

function HowItWorks() {
  const steps: [string, string][] = [
    ["Søk", "Finn NRK-podkasten du vil høre på."],
    ["Kopier", "Kopier RSS-lenken til podkasten."],
    ["Lim inn", "Legg til lenken i podkast-appen din – ferdig!"],
  ];

  return (
    <>
      <section class="mt-8 flex flex-wrap justify-center gap-2">
        {SUGGESTIONS.map((suggestion) => (
          <a
            key={suggestion}
            href={`/?query=${encodeURIComponent(suggestion)}`}
            class="rounded-full border border-line dark:border-line-dark bg-paper-raised dark:bg-paper-raised-dark px-4 py-1.5 text-sm hover:border-accent hover:text-accent dark:hover:border-accent-dark dark:hover:text-accent-dark transition-colors"
          >
            {suggestion}
          </a>
        ))}
      </section>

      <section class="mt-14">
        <h2 class="font-display text-2xl font-bold text-center mb-8">Slik funker det</h2>
        <ol class="grid sm:grid-cols-3 gap-4">
          {steps.map(([title, description], index) => (
            <li
              key={title}
              class="rounded-3xl bg-paper-raised dark:bg-paper-raised-dark border border-line dark:border-line-dark p-5"
            >
              <span class="grid place-items-center size-8 rounded-full bg-accent/10 text-accent dark:bg-accent-dark/15 dark:text-accent-dark font-display font-bold mb-3">
                {index + 1}
              </span>
              <h3 class="font-display font-bold">{title}</h3>
              <p class="mt-1 text-sm text-ink-soft dark:text-ink-soft-dark">{description}</p>
            </li>
          ))}
        </ol>
        <p class="mt-6 text-sm text-center text-ink-soft dark:text-ink-soft-dark">
          Usikker på hvordan du legger til en RSS-lenke?{" "}
          <a
            href="https://help.omnystudio.com/en/articles/5222518-podcast-apps-that-support-add-rss-feed"
            class="underline underline-offset-4 decoration-line dark:decoration-line-dark hover:text-accent dark:hover:text-accent-dark"
          >
            Se guide for populære apper
          </a>
        </p>
      </section>
    </>
  );
}
