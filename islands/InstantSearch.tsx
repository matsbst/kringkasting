import { useEffect, useRef, useState } from "preact/hooks";
import type { SeriesSummary } from "../lib/series-summary.ts";
import SeriesCard from "../components/SeriesCard.tsx";
import { IconLoader, IconSearch } from "../components/icons.tsx";

const DEBOUNCE_MS = 300;
const MIN_QUERY_LENGTH = 2;

const SUGGESTIONS = [
  "Oppdatert",
  "Hele historien",
  "Radioresepsjonen",
  "Abels tårn",
  "Trygdekontoret",
  "Berrum & Beyer",
];

type Props = {
  initialQuery: string | null;
  initialResults: SeriesSummary[] | null;
  origin: string;
};

/**
 * Search-as-you-type. Progressive enhancement over the plain GET form:
 * the server renders initial results for direct loads and no-JS clients,
 * then this island takes over with debounced fetches to /api/search.
 */
type Hits = { query: string; items: SeriesSummary[] };

export default function InstantSearch(props: Props) {
  const [query, setQuery] = useState(props.initialQuery ?? "");
  // results are stored WITH the query that produced them, so stale hits
  // are never labeled with a newer query while a fetch is in flight
  const [hits, setHits] = useState<Hits | null>(
    props.initialResults !== null && props.initialQuery
      ? { query: props.initialQuery.trim(), items: props.initialResults }
      : null,
  );
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  const abortRef = useRef<AbortController | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    return () => {
      clearTimeout(timerRef.current);
      abortRef.current?.abort();
    };
  }, []);

  const syncUrl = (trimmed: string) => {
    const url = trimmed === "" ? "/" : `/?query=${encodeURIComponent(trimmed)}`;
    history.replaceState(null, "", url);
  };

  const runSearch = async (rawQuery: string) => {
    clearTimeout(timerRef.current);
    abortRef.current?.abort();

    const trimmed = rawQuery.trim();
    syncUrl(trimmed);

    if (trimmed.length < MIN_QUERY_LENGTH) {
      setHits(null);
      setLoading(false);
      setFailed(false);
      return;
    }

    const controller = new AbortController();
    abortRef.current = controller;
    setLoading(true);
    setFailed(false);

    try {
      const response = await fetch(`/api/search?q=${encodeURIComponent(trimmed)}`, {
        signal: controller.signal,
      });
      if (!response.ok) {
        throw new Error(`search failed with ${response.status}`);
      }
      const data = (await response.json()) as { results: SeriesSummary[] };
      setHits({ query: trimmed, items: data.results });
    } catch (error) {
      if (!controller.signal.aborted) {
        console.error(error);
        setHits(null);
        setFailed(true);
      }
    } finally {
      if (!controller.signal.aborted) {
        setLoading(false);
      }
    }
  };

  const onInput = (value: string) => {
    setQuery(value);
    clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => runSearch(value), DEBOUNCE_MS);
  };

  const searchNow = (value: string) => {
    setQuery(value);
    runSearch(value);
  };

  const showResults = query.trim().length >= MIN_QUERY_LENGTH && hits !== null;

  return (
    <div>
      <form
        action="/"
        method="get"
        onSubmit={(event) => {
          event.preventDefault();
          searchNow(query);
        }}
        class="w-full"
      >
        <label class="sr-only" htmlFor="query">Søk etter NRK-podkast</label>
        <div class="relative">
          <span class="absolute left-5 top-1/2 -translate-y-1/2 text-ink-soft dark:text-ink-soft-dark pointer-events-none">
            {loading ? <IconLoader size={22} class="animate-spin" /> : <IconSearch size={22} />}
          </span>
          <input
            type="search"
            id="query"
            name="query"
            placeholder="Søk etter en NRK-podkast …"
            value={query}
            onInput={(event) => onInput(event.currentTarget.value)}
            autocomplete="off"
            class="w-full rounded-full border-2 border-line dark:border-line-dark bg-paper-raised dark:bg-paper-raised-dark pl-14 pr-28 py-4 text-lg placeholder:text-ink-soft/70 dark:placeholder:text-ink-soft-dark/70 focus:outline-none focus:border-accent dark:focus:border-accent-dark shadow-sm"
          />
          <button
            type="submit"
            class="absolute right-2 top-1/2 -translate-y-1/2 rounded-full bg-accent hover:bg-accent-strong dark:bg-accent-dark dark:hover:bg-accent text-white dark:text-paper-dark font-semibold px-6 py-2.5 transition-colors cursor-pointer"
          >
            Søk
          </button>
        </div>
      </form>

      {failed && (
        <section class="mt-12 text-center space-y-2">
          <h2 class="font-display text-2xl font-bold">Søket feilet</h2>
          <p class="text-ink-soft dark:text-ink-soft-dark">Prøv igjen om et lite øyeblikk.</p>
        </section>
      )}

      {showResults && hits !== null && !failed && (
        hits.items.length === 0
          ? (
            <section class="mt-12 text-center space-y-2">
              <h2 class="font-display text-2xl font-bold">Ingen treff for «{hits.query}»</h2>
              <p class="text-ink-soft dark:text-ink-soft-dark">
                Prøv et annet søkeord, eller sjekk skrivemåten.
              </p>
            </section>
          )
          : (
            <section class="mt-10" aria-live="polite">
              <h2 class="sr-only">Søkeresultat</h2>
              <p class="text-sm text-ink-soft dark:text-ink-soft-dark mb-4">
                {hits.items.length} treff for «{hits.query}»
              </p>
              <div class="space-y-4">
                {hits.items.map((serie) => <SeriesCard key={serie.seriesId} serie={serie} origin={props.origin} />)}
              </div>
            </section>
          )
      )}

      {!showResults && !failed && <HowItWorks onSuggestion={searchNow} />}
    </div>
  );
}

function HowItWorks(props: { onSuggestion: (query: string) => void }) {
  const steps: [string, string][] = [
    ["Søk", "Finn NRK-podkasten du vil høre på."],
    ["Kopier", "Kopier RSS-lenken til podkasten."],
    ["Lim inn", "Legg til lenken i podkast-appen din – ferdig!"],
  ];

  return (
    <>
      <section class="mt-8 flex flex-wrap justify-center gap-2">
        {SUGGESTIONS.map((suggestion) => (
          <button
            type="button"
            key={suggestion}
            onClick={() => props.onSuggestion(suggestion)}
            class="rounded-full border border-line dark:border-line-dark bg-paper-raised dark:bg-paper-raised-dark px-4 py-1.5 text-sm hover:border-accent hover:text-accent dark:hover:border-accent-dark dark:hover:text-accent-dark transition-colors cursor-pointer"
          >
            {suggestion}
          </button>
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
