import { nrkRadio } from "./nrk/nrk.ts";
import { Series, storage } from "./storage.ts";
import * as datetime from "@std/datetime";
import { Buffer } from "node:buffer";

const SYNC_INTERVAL_HOURS = 1;
const DENO_KV_MAX_BYTES = 65_536;

/** remember ids NRK doesn't know for a while, so garbage requests are cheap */
const NOT_FOUND_TTL_MS = 10 * 60 * 1000;
const notFoundUntil = new Map<string, number>();

/** coalesce concurrent requests for the same series into one NRK fetch */
const inFlight = new Map<string, Promise<Series | null>>();

async function initialFetch(options: { id: string }): Promise<Series | null> {
  const series = await nrkRadio.getSeries(options.id);
  if (!series) {
    return null;
  }

  const trimmed = trimSeriesToSize(series, DENO_KV_MAX_BYTES);
  const stored = await storage.writeSeries(trimmed);
  if (!stored) {
    // still serve the fetched data; only persisting failed
    console.error(`Failed to store series ${options.id}`);
  }

  return trimmed;
}

async function updateFetch(existingSeries: Series): Promise<Series> {
  const knownEpisodeIds = new Set(existingSeries.episodes.map((episode) => episode.id));
  const update = await nrkRadio.getSeries(existingSeries.id, knownEpisodeIds);
  if (!update) {
    // NRK outage or rate limiting: serve the stale copy rather than
    // pretending the series disappeared
    console.error(`Failed to refresh series ${existingSeries.id}, serving stale data`);
    return existingSeries;
  }

  /**
   * `update.episodes` only contains episodes we did not already know
   * about. Since we don't control the API, we should not make
   * assumptions about the order, but rather sort to what we want.
   */
  const episodesSortedDescending = [...update.episodes, ...existingSeries.episodes]
    .sort((a, b) => a.date.getTime() > b.date.getTime() ? -1 : 1);

  const refreshed: Series = {
    ...update,
    lastFetchedAt: new Date(),
    episodes: episodesSortedDescending,
  };

  /**
   * The KV store has a limit of 64kb per value.
   * A pragmatic (not perfect) solution is to trim the series
   * down until we're within the limit.
   */
  const trimmed = trimSeriesToSize(refreshed, DENO_KV_MAX_BYTES);

  const updateSuccessful = await storage.writeSeries(trimmed);
  if (!updateSuccessful) {
    console.error(`Failed to update series ${existingSeries.id}`);
  }

  return trimmed;
}

function trimSeriesToSize(series: Series, bytes: number): Series {
  let episodes = series.episodes;
  let trimmed = series;
  while (Buffer.byteLength(JSON.stringify(trimmed)) > bytes && episodes.length > 0) {
    episodes = episodes.slice(0, -1);
    trimmed = { ...series, episodes };
  }
  return trimmed;
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
  const seriesFromStorage = await storage.readSeries(options);

  /**
   * We don't have the feed in storage,
   * and we need to fetch it for the first time.
   */
  if (seriesFromStorage === null) {
    return await initialFetch(options);
  }

  // we have the feed in storage and it's not too old
  if (isSeriesFromStorageNew(seriesFromStorage)) {
    return seriesFromStorage;
  }

  /**
   * We have the feed in storage, but it's too old
   * and needs to be refreshed.
   */
  return await updateFetch(seriesFromStorage);
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
};

export const forTestingOnly = {
  getTimeSinceLastFetch,
  isSeriesFromStorageNew,
  trimSeriesToSize,
};
