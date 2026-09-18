import { nrkRadio } from "./nrk/nrk.ts";
import { parseFeedId } from "./feed-id.ts";
import { Series, storage } from "./storage.ts";
import { enqueueBacklogCrawl } from "./backlog.ts";
import { canRetry, clearRetry, deferRetry } from "./retry.ts";
import * as datetime from "@std/datetime";

const SYNC_INTERVAL_HOURS = 3;

/** how often title/artwork is re-fetched from NRK */
const METADATA_TTL_MS = 7 * 24 * 60 * 60 * 1000;

/** remember ids NRK doesn't know for a while, so garbage requests are cheap */
const NOT_FOUND_TTL_MS = 10 * 60 * 1000;
const notFoundUntil = new Map<string, number>();

/** coalesce concurrent requests for the same series into one NRK fetch */
const inFlight = new Map<string, Promise<Series | null>>();

/** season feeds resolve via NRK's season endpoint, everything else via the series catalog */
function fetchFromNrk(
  feedId: string,
  options: Parameters<typeof nrkRadio.getSeries>[1],
): ReturnType<typeof nrkRadio.getSeries> {
  const { seriesId, seasonId } = parseFeedId(feedId);
  return seasonId === null ? nrkRadio.getSeries(feedId, options) : nrkRadio.getSeason(seriesId, seasonId, options);
}

async function initialFetch(options: { id: string }): Promise<Series | null> {
  const series = await fetchFromNrk(options.id, {
    onEpisodeFailure: (episodeId) => storage.recordEpisodeFailure(options.id, episodeId),
  });
  if (series === "error") {
    // upstream outage: surface it (route answers 502) instead of letting
    // the negative cache turn a hiccup into ten minutes of 404s
    deferRetry(`refresh:${options.id}`);
    throw new Error(`NRK unavailable while fetching ${options.id}`);
  }
  if (!series) {
    clearRetry(`refresh:${options.id}`);
    return null;
  }

  if (!storage.writeSeries(series)) {
    // still serve the fetched data; only persisting failed
    console.error(`Failed to store series ${options.id}`);
    return series;
  }

  clearRetry(`refresh:${options.id}`);
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
  // series with unknown catalog kind take the full path; season feeds do
  // too (their listing is oldest-first, so the newest-first early-stop
  // in getNewEpisodes doesn't apply — and a full season walk is small).
  const metadataAge = Date.now() - (existingSeries.metadataRefreshedAt?.getTime() ?? 0);
  const cheapRefresh = !existingSeries.isUmbrella &&
    parseFeedId(existingSeries.id).seasonId === null &&
    existingSeries.catalogKind;

  if (cheapRefresh) {
    const progressKey = `refresh-progress:${existingSeries.id}`;
    const progress = storage.readState<{ cursor: string; stopEpisodeIds: string[] }>(progressKey);
    if (!progress && metadataAge >= METADATA_TTL_MS) {
      const metadata = await nrkRadio.getSeries(existingSeries.id, {
        catalogKind: existingSeries.catalogKind,
        metadataOnly: true,
      });
      if (!metadata || metadata === "error") {
        deferRetry(`refresh:${existingSeries.id}`);
        return existingSeries;
      }
      metadata.lastFetchedAt = existingSeries.lastFetchedAt;
      if (!storage.writeSeries(metadata)) {
        deferRetry(`refresh:${existingSeries.id}`);
        return existingSeries;
      }
    }
    // Keep the boundary from BEFORE this batch. Newly persisted episodes
    // must not stop a resumed refresh on its overlap page.
    const stopEpisodeIds = progress?.stopEpisodeIds ??
      existingSeries.episodes.slice(0, 50).map((episode) => episode.id);
    // Save the original boundary before committing any new episodes so a
    // process exit between episode persistence and checkpoint advancement is safe.
    storage.writeState(progressKey, {
      cursor: progress?.cursor ??
        `/radio/catalog/${existingSeries.catalogKind}/${existingSeries.id}/episodes?page=1&pageSize=50`,
      stopEpisodeIds,
    });
    const result = await nrkRadio.getNewEpisodes(existingSeries.id, existingSeries.catalogKind!, {
      skipEpisodeIds,
      stopEpisodeIds: new Set(stopEpisodeIds),
      cursorHref: progress?.cursor,
      onEpisodeFailure,
    });
    if (result === null) {
      deferRetry(`refresh:${existingSeries.id}`);
      return existingSeries;
    }
    if (
      result.episodes.length > 0 && !storage.addEpisodes(existingSeries.id, result.episodes.map(nrkRadio.parseEpisode))
    ) {
      deferRetry(`refresh:${existingSeries.id}`);
      return existingSeries;
    }
    if (!result.sawAllPages && result.resumeHref) {
      storage.writeState(progressKey, { cursor: result.resumeHref, stopEpisodeIds });
      deferRetry(`refresh:${existingSeries.id}`);
    } else {
      storage.deleteState(progressKey);
      clearRetry(`refresh:${existingSeries.id}`);
      storage.touchLastFetched(existingSeries.id);
    }
    return storage.readSeries({ id: existingSeries.id }) ?? existingSeries;
  }

  const update = await fetchFromNrk(existingSeries.id, {
    skipEpisodeIds,
    onEpisodeFailure,
    catalogKind: existingSeries.catalogKind,
  });
  if (!update || update === "error") {
    // NRK outage or rate limiting: serve the stale copy rather than
    // pretending the series disappeared
    console.error(`Failed to refresh series ${existingSeries.id}, serving stale data`);
    deferRetry(`refresh:${existingSeries.id}`);
    return existingSeries;
  }

  // upserts metadata + new episodes and renews lastFetchedAt;
  // existing episodes are kept, so the archive accumulates
  if (!storage.writeSeries(update)) {
    console.error(`Failed to update series ${existingSeries.id}`);
    deferRetry(`refresh:${existingSeries.id}`);
    return existingSeries;
  }

  clearRetry(`refresh:${existingSeries.id}`);
  return storage.readSeries({ id: existingSeries.id }) ?? existingSeries;
}

/**
 * Refresh cadence adapts to how actively a show publishes: every three hours while
 * it's putting out episodes, weekly once it's long dormant. A small random
 * jitter keeps refreshes from clustering at the top of the hour.
 */
function intervalHoursFor(newestEpisodeAt: Date | null | undefined): number {
  if (!newestEpisodeAt) {
    return SYNC_INTERVAL_HOURS;
  }
  const ageDays = (Date.now() - newestEpisodeAt.getTime()) / (24 * 60 * 60 * 1000);
  if (ageDays < 2) {
    // recently-active shows (daily news etc.) refresh hourly
    return 1;
  }
  if (ageDays < 30) {
    return 12;
  }
  if (ageDays < 180) return 72;
  return 168;
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
  if (storage.readState(`refresh-progress:${seriesId}`) && canRetry(`refresh:${seriesId}`)) return false;
  if (!meta.backlogComplete && canRetry(`backlog:${seriesId}`)) return false;
  if (!canRetry(`refresh:${seriesId}`)) return true;
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
  if (!canRetry(`refresh:${options.id}`)) {
    if (!seriesFromStorage) throw new Error("Refresh is waiting for its retry deadline");
    series = seriesFromStorage;
  } else if (seriesFromStorage === null) {
    // first time we see this feed
    series = await initialFetch(options);
  } else if (
    !storage.readState(`refresh-progress:${options.id}`) &&
    isSeriesFromStorageNew(seriesFromStorage, refreshIntervalWithJitter(seriesFromStorage))
  ) {
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

async function getSeries(options: { id: string }): Promise<Series | null> {
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
  intervalHoursFor,
  getTimeSinceLastFetch,
  isSeriesFromStorageNew,
};
