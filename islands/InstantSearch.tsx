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
          <span class="absolute left-4 top-1/2 -translate-y-1/2 text-ink-3 dark:text-ink-3-dark pointer-events-none">
            {loading ? <IconLoader size={18} class="animate-spin" /> : <IconSearch size={18} />}
          </span>
          <input
            type="search"
            id="query"
            name="query"
            placeholder="Søk etter en NRK-podkast"
            value={query}
            onInput={(event) => onInput(event.currentTarget.value)}
            autocomplete="off"
            class="w-full h-12 rounded-lg border border-line-strong dark:border-line-strong-dark bg-canvas dark:bg-canvas-dark pl-11 pr-20 text-base placeholder:text-ink-3 dark:placeholder:text-ink-3-dark focus:outline-none focus:border-ink dark:focus:border-ink-dark"
          />
          <button
            type="submit"
            class="absolute right-1.5 top-1/2 -translate-y-1/2 rounded-md bg-ink text-canvas dark:bg-ink-dark dark:text-canvas-dark hover:bg-ink/80 dark:hover:bg-ink-dark/85 text-sm font-medium px-4 py-2 transition-colors cursor-pointer"
          >
            Søk
          </button>
        </div>
      </form>

      {failed && (
        <section class="mt-14 space-y-1">
          <h2 class="text-xl font-medium">Søket feilet</h2>
          <p class="text-ink-2 dark:text-ink-2-dark">Prøv igjen om et lite øyeblikk.</p>
        </section>
      )}

      {showResults && hits !== null && !failed && (
        hits.items.length === 0
          ? (
            <section class="mt-14 space-y-1">
              <h2 class="text-xl font-medium">Ingen treff for «{hits.query}»</h2>
              <p class="text-ink-2 dark:text-ink-2-dark">
                Prøv et annet søkeord, eller sjekk skrivemåten.
              </p>
            </section>
          )
          : (
            <section class="mt-10" aria-live="polite">
              <h2 class="sr-only">Søkeresultat</h2>
              <p class="text-sm text-ink-2 dark:text-ink-2-dark mb-8">
                {hits.items.length} treff for «{hits.query}»
              </p>
              <div class="space-y-10">
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
      <section class="mt-5 flex flex-wrap gap-2">
        {SUGGESTIONS.map((suggestion) => (
          <button
            type="button"
            key={suggestion}
            onClick={() => props.onSuggestion(suggestion)}
            class="rounded-full border border-line dark:border-line-dark px-3.5 py-1.5 text-sm text-ink-2 dark:text-ink-2-dark hover:bg-hover dark:hover:bg-hover-dark hover:text-ink dark:hover:text-ink-dark transition-colors cursor-pointer"
          >
            {suggestion}
          </button>
        ))}
      </section>

      <section class="mt-24">
        <h2 class="text-xl font-medium mb-8">Slik funker det</h2>
        <ol class="grid sm:grid-cols-3 gap-x-8 gap-y-6">
          {steps.map(([title, description], index) => (
            <li key={title}>
              <p class="text-sm text-ink-3 dark:text-ink-3-dark tabular-nums">{index + 1}</p>
              <h3 class="mt-1 font-medium">{title}</h3>
              <p class="mt-1 text-sm text-ink-2 dark:text-ink-2-dark">{description}</p>
            </li>
          ))}
        </ol>
        <p class="mt-10 text-sm text-ink-2 dark:text-ink-2-dark">
          Usikker på hvordan du legger til en RSS-lenke?{" "}
          <a
            href="https://help.omnystudio.com/en/articles/5222518-podcast-apps-that-support-add-rss-feed"
            class="underline underline-offset-2 hover:text-ink dark:hover:text-ink-dark"
          >
            Se guide for populære apper
          </a>
        </p>
      </section>
    </>
  );
}
