import { get } from "./http.ts";
import { storage } from "./storage.ts";
import { canRetry, clearRetry, deferRetry } from "./retry.ts";

/**
 * A weekly-refreshed, persistent list of every podcast in NRK's catalog,
 * used to serve rotating suggestions on the front page. Loaded lazily
 * in the background so no request ever waits for the ~13 paged fetches.
 */

type CatalogEntry = { seriesId: string; title: string };

type CatalogPage = {
  _links?: { nextPage?: { href: string } };
  series?: { seriesId?: string; title?: string; type?: string }[];
};

const nrkAPI = "https://psapi.nrk.no";
const CATALOG_TTL_MS = 7 * 24 * 60 * 60 * 1000;
const MAX_PAGES = 30;

let cache: { expires: number; entries: CatalogEntry[] } | null = null;
let loading: Promise<void> | null = null;
let restored = false;
function restoreCache() {
  if (!restored) {
    cache = storage.readState("catalog");
    restored = true;
  }
}

async function loadCatalog() {
  const entries: CatalogEntry[] = [];
  const seen = new Set<string>();
  let href: string | null = "/radio/search/categories/podcast?take=100";

  for (let page = 0; page < MAX_PAGES && href; page++) {
    const body: CatalogPage | null = (await get<CatalogPage>(`${nrkAPI}${href}`)).body;
    if (!body) {
      // Keep the previous complete catalog; retry later.
      deferRetry("catalog");
      return;
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

  if (href || entries.length === 0) {
    deferRetry("catalog");
    return;
  }
  if (entries.length > 0) {
    cache = { expires: Date.now() + CATALOG_TTL_MS, entries };
    storage.writeState("catalog", cache);
    clearRetry("catalog");
    console.log(`Catalog loaded: ${entries.length} podcasts`);
  }
}

function refreshInBackground() {
  if (!loading && canRetry("catalog")) {
    loading = loadCatalog()
      .catch((error) => {
        deferRetry("catalog");
        console.error(`Catalog load failed: ${error}`);
      })
      .finally(() => {
        loading = null;
      });
  }
}

/** number of shows in NRK's catalog, when loaded */
export function getCatalogSize(): number | null {
  restoreCache();
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
  restoreCache();
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

export const forTestingOnly = {
  loadCatalog,
  resetMemory: () => {
    cache = null;
    restored = false;
  },
};
