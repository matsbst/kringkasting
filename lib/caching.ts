import { nrkRadio } from "./nrk/nrk.ts";
import { Series, storage } from "./storage.ts";
import { enqueueBacklogCrawl } from "./backlog.ts";
import { recordGcRun } from "./stats.ts";
import * as datetime from "@std/datetime";

const SYNC_INTERVAL_HOURS = 1;

/** how often title/artwork is re-fetched from NRK */
const METADATA_TTL_MS = 7 * 24 * 60 * 60 * 1000;

/** remember ids NRK doesn't know for a while, so garbage requests are cheap */
const NOT_FOUND_TTL_MS = 10 * 60 * 1000;
const notFoundUntil = new Map<string, number>();

/** coalesce concurrent requests for the same series into one NRK fetch */
const inFlight = new Map<string, Promise<Series | null>>();

async function initialFetch(options: { id: string }): Promise<Series | null> {
  const series = await nrkRadio.getSeries(options.id, {
    onEpisodeFailure: (episodeId) => storage.recordEpisodeFailure(options.id, episodeId),
  });
  if (series === "error") {
    // upstream outage: surface it (route answers 502) instead of letting
    // the negative cache turn a hiccup into ten minutes of 404s
    throw new Error(`NRK unavailable while fetching ${options.id}`);
  }
  if (!series) {
    return null;
  }

  if (!storage.writeSeries(series)) {
    // still serve the fetched data; only persisting failed
    console.error(`Failed to store series ${options.id}`);
    return series;
  }

  return storage.readSeries(options) ?? series;
}

async function updateFetch(existingSeries: Series): Promise<Series> {
  // incremental: only NEW episodes get playback-manifest lookups, and
  // episodes under failure backoff are not re-checked every refresh
  const skipEpisodeIds = new Set(existingSeries.episodes.map((episode) => episode.id));
  for (const blocked of storage.readBlockedEpisodeIds(existingSeries.id)) {
    skipEpisodeIds.add(blocked);
  }
  const onEpisodeFailure = (episodeId: string) => storage.recordEpisodeFailure(existingSeries.id, episodeId);

  // metadata (title/artwork) changes rarely: most refreshes only need the
  // episode listing (one NRK request instead of two). Umbrella shows and
  // series with unknown catalog kind take the full path.
  const metadataAge = Date.now() - (existingSeries.metadataRefreshedAt?.getTime() ?? 0);
  const cheapRefresh = !existingSeries.isUmbrella &&
    existingSeries.catalogKind &&
    metadataAge < METADATA_TTL_MS;

  if (cheapRefresh) {
    const result = await nrkRadio.getNewEpisodes(existingSeries.id, existingSeries.catalogKind!, {
      skipEpisodeIds,
      onEpisodeFailure,
    });
    if (result === null) {
      console.error(`Failed to refresh series ${existingSeries.id}, serving stale data`);
      return existingSeries;
    }
    if (result.episodes.length > 0) {
      storage.addEpisodes(existingSeries.id, result.episodes.map(nrkRadio.parseEpisode));
    }
    if (!result.sawAllPages) {
      // pagination broke mid-burst: re-open the archive crawl so the
      // episodes beyond the break get fetched (the next refresh alone
      // would stop at page one, which is now known)
      console.error(`Refresh of ${existingSeries.id} was partial, re-opening archive crawl`);
      storage.setBacklogState(existingSeries.id, null, false);
      enqueueBacklogCrawl(existingSeries.id);
    }
    storage.touchLastFetched(existingSeries.id);
    return storage.readSeries({ id: existingSeries.id }) ?? existingSeries;
  }

  const update = await nrkRadio.getSeries(existingSeries.id, {
    skipEpisodeIds,
    onEpisodeFailure,
    catalogKind: existingSeries.catalogKind,
  });
  if (!update || update === "error") {
    // NRK outage or rate limiting: serve the stale copy rather than
    // pretending the series disappeared
    console.error(`Failed to refresh series ${existingSeries.id}, serving stale data`);
    return existingSeries;
  }

  // upserts metadata + new episodes and renews lastFetchedAt;
  // existing episodes are kept, so the archive accumulates
  if (!storage.writeSeries(update)) {
    console.error(`Failed to update series ${existingSeries.id}`);
    return existingSeries;
  }

  return storage.readSeries({ id: existingSeries.id }) ?? existingSeries;
}

/**
 * Refresh cadence adapts to how actively a show publishes: hourly while
 * it's putting out episodes, daily once it's dormant. A small random
 * jitter keeps refreshes from clustering at the top of the hour.
 */
function intervalHoursFor(newestEpisodeAt: Date | null | undefined): number {
  if (!newestEpisodeAt) {
    return SYNC_INTERVAL_HOURS;
  }
  const ageDays = (Date.now() - newestEpisodeAt.getTime()) / (24 * 60 * 60 * 1000);
  if (ageDays < 2) {
    return 1;
  }
  if (ageDays < 30) {
    return 6;
  }
  return 24;
}

function refreshIntervalHours(series: Series): number {
  return intervalHoursFor(series.episodes.at(0)?.date);
}

/**
 * Cheap freshness probe for the feed fast path: true only when the
 * series is comfortably inside its refresh window (the conservative end
 * of the jitter range), so the fast path never serves anything the slow
 * path would have refreshed.
 */
function metaIsFresh(seriesId: string): boolean {
  const meta = storage.readSeriesMeta(seriesId);
  if (!meta) {
    return false;
  }
  const ageHours = (Date.now() - meta.lastFetchedAt.getTime()) / (60 * 60 * 1000);
  return ageHours <= intervalHoursFor(meta.newestEpisodeAt) * 0.85;
}

function refreshIntervalWithJitter(series: Series): number {
  return refreshIntervalHours(series) * (0.85 + Math.random() * 0.3);
}

function getTimeSinceLastFetch(inputDate: Date, againstDate = new Date()): number | null {
  return datetime.difference(inputDate, againstDate, { units: ["hours"] }).hours ?? null;
}

function isSeriesFromStorageNew(
  seriesFromStorage: Series,
  syncInterval = SYNC_INTERVAL_HOURS,
  timeSinceLastFetch = getTimeSinceLastFetch(seriesFromStorage.lastFetchedAt),
) {
  return !Number.isNaN(timeSinceLastFetch) &&
    seriesFromStorage !== null &&
    timeSinceLastFetch !== null &&
    timeSinceLastFetch !== undefined &&
    timeSinceLastFetch <= syncInterval;
}

async function fetchSeries(options: { id: string }): Promise<Series | null> {
  const seriesFromStorage = storage.readSeries(options);

  let series: Series | null;
  if (seriesFromStorage === null) {
    // first time we see this feed
    series = await initialFetch(options);
  } else if (isSeriesFromStorageNew(seriesFromStorage, refreshIntervalWithJitter(seriesFromStorage))) {
    // cached and fresh
    series = seriesFromStorage;
  } else {
    // cached but stale
    series = await updateFetch(seriesFromStorage);
  }

  // make sure the full archive gets (or resumes getting) crawled
  if (series && !series.backlogComplete) {
    enqueueBacklogCrawl(series.id);
  }

  return series;
}

function pruneExpired(map: Map<string, number>) {
  if (map.size < 500) {
    return;
  }
  const now = Date.now();
  for (const [key, expiry] of map) {
    if (expiry <= now) {
      map.delete(key);
    }
  }
}

/** stale series are garbage-collected once a day */
const GC_MAX_AGE_MS = 90 * 24 * 60 * 60 * 1000;
const GC_INTERVAL_MS = 24 * 60 * 60 * 1000;
let lastGcAt = 0;

function maybeCollectGarbage() {
  if (Date.now() - lastGcAt < GC_INTERVAL_MS) {
    return;
  }
  lastGcAt = Date.now();
  const deleted = storage.deleteStaleSeries(GC_MAX_AGE_MS);
  recordGcRun(deleted);
  if (deleted > 0) {
    console.log(`Garbage collected ${deleted} series not requested in 90 days`);
  }
}

async function getSeries(options: { id: string }): Promise<Series | null> {
  maybeCollectGarbage();

  const missUntil = notFoundUntil.get(options.id);
  if (missUntil !== undefined) {
    if (missUntil > Date.now()) {
      return null;
    }
    notFoundUntil.delete(options.id);
  }

  let promise = inFlight.get(options.id);
  if (!promise) {
    promise = fetchSeries(options).finally(() => inFlight.delete(options.id));
    inFlight.set(options.id, promise);
  }

  const series = await promise;
  if (series === null) {
    pruneExpired(notFoundUntil);
    notFoundUntil.set(options.id, Date.now() + NOT_FOUND_TTL_MS);
  }
  return series;
}

export const caching = {
  getSeries,
  metaIsFresh,
};

export const forTestingOnly = {
  getTimeSinceLastFetch,
  isSeriesFromStorageNew,
};
