import { Series, storage } from "./storage.ts";
import { rss } from "./rss.ts";
import { etagFor } from "./utils.ts";

/**
 * Memoizes the rendered feed XML (and its ETag) per series, invalidated
 * via the storage data version whenever the series' content changes.
 * Serving a poll — 200 or 304 — then costs no serialization or hashing.
 */

export type RenderedFeed = {
  xml: string;
  etag: string;
  lastModified: Date;
};

type CacheEntry = RenderedFeed & { version: number };

const MAX_ENTRIES = 500;
const cache = new Map<string, CacheEntry>();

export async function renderFeed(series: Series, origin: string): Promise<RenderedFeed> {
  const key = `${series.id}|${origin}`;
  const version = storage.getDataVersion(series.id);

  const cached = cache.get(key);
  if (cached && cached.version === version) {
    return cached;
  }

  const xml = rss.assembleFeed(series, origin);
  const etag = await etagFor(xml);
  // episodes are sorted newest first; fall back to the fetch time
  const lastModified = series.episodes.at(0)?.date ?? series.lastFetchedAt;

  if (cache.size >= MAX_ENTRIES) {
    // drop the oldest entry (Map preserves insertion order)
    const oldest = cache.keys().next().value;
    if (oldest !== undefined) {
      cache.delete(oldest);
    }
  }

  const entry: CacheEntry = { version, xml, etag, lastModified };
  cache.set(key, entry);
  return entry;
}
