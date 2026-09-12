import { get } from "./http.ts";

/**
 * A daily-refreshed in-memory list of every podcast in NRK's catalog,
 * used to serve rotating suggestions on the front page. Loaded lazily
 * in the background so no request ever waits for the ~13 paged fetches.
 */

type CatalogEntry = { seriesId: string; title: string };

type CatalogPage = {
  _links?: { nextPage?: { href: string } };
  series?: { seriesId?: string; title?: string; type?: string }[];
};

const nrkAPI = "https://psapi.nrk.no";
const CATALOG_TTL_MS = 24 * 60 * 60 * 1000;
const MAX_PAGES = 30;

let cache: { expires: number; entries: CatalogEntry[] } | null = null;
let loading: Promise<void> | null = null;

async function loadCatalog() {
  const entries: CatalogEntry[] = [];
  const seen = new Set<string>();
  let href: string | null = "/radio/search/categories/podcast?take=100";

  for (let page = 0; page < MAX_PAGES && href; page++) {
    const body: CatalogPage | null = (await get<CatalogPage>(`${nrkAPI}${href}`)).body;
    if (!body) {
      // NRK hiccup: keep whatever we already collected
      break;
    }
    for (const item of body.series ?? []) {
      // customSeason rows are seasons of umbrella shows, not series
      if (!item.seriesId || !item.title || item.type === "customSeason" || seen.has(item.seriesId)) {
        continue;
      }
      seen.add(item.seriesId);
      entries.push({ seriesId: item.seriesId, title: item.title });
    }
    href = body._links?.nextPage?.href ?? null;
  }

  if (entries.length > 0) {
    cache = { expires: Date.now() + CATALOG_TTL_MS, entries };
    console.log(`Catalog loaded: ${entries.length} podcasts`);
  }
}

function refreshInBackground() {
  if (!loading) {
    loading = loadCatalog()
      .catch((error) => console.error(`Catalog load failed: ${error}`))
      .finally(() => {
        loading = null;
      });
  }
}

/** number of shows in NRK's catalog, when loaded */
export function getCatalogSize(): number | null {
  if (!cache || cache.expires < Date.now()) {
    refreshInBackground();
  }
  return cache?.entries.length ?? null;
}

/**
 * Random show titles for the suggestion chips. Returns null until the
 * catalog has loaded at least once (callers fall back to a static list);
 * a stale cache is served while refreshing.
 */
export function getRandomShowTitles(count: number): string[] | null {
  if (!cache || cache.expires < Date.now()) {
    refreshInBackground();
  }
  if (!cache) {
    return null;
  }

  // long titles blow up the chip layout
  const pool = cache.entries.filter((entry) => entry.title.length <= 24);
  const picks: string[] = [];
  const used = new Set<number>();
  while (picks.length < count && used.size < pool.length) {
    const index = Math.floor(Math.random() * pool.length);
    if (used.has(index)) {
      continue;
    }
    used.add(index);
    picks.push(pool[index].title);
  }
  return picks;
}
