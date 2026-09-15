import { assertEquals } from "@std/assert";
import { storage } from "./storage.ts";
import { testUtils } from "./test-utils.ts";

// deno task test sets NRSS_DB_PATH=:memory:

Deno.test("can store and retrieve a series", () => {
  const series = testUtils.generateSeries();
  storage.writeSeries(series);

  const readSeries = storage.readSeries(series);
  assertEquals(readSeries?.id, series.id);
  assertEquals(readSeries?.title, series.title);
  assertEquals(readSeries?.imageUrl, series.imageUrl);
  assertEquals(readSeries?.episodes.length, series.episodes.length);
  assertEquals(readSeries?.backlogComplete, false);
});

Deno.test("episodes are returned newest first", () => {
  const series = testUtils.generateSeries();
  series.episodes = [
    testUtils.generateEpisode({ date: new Date("2024-01-01") }),
    testUtils.generateEpisode({ date: new Date("2024-03-01") }),
    testUtils.generateEpisode({ date: new Date("2024-02-01") }),
  ];
  storage.writeSeries(series);

  const readSeries = storage.readSeries(series);
  const dates = readSeries!.episodes.map((episode) => episode.date.getTime());
  assertEquals(dates, [...dates].sort((a, b) => b - a));
});

Deno.test("writing a series again keeps episodes missing from the update (archive accumulation)", () => {
  const series = testUtils.generateSeries();
  const oldEpisode = testUtils.generateEpisode({ date: new Date("2020-01-01") });
  const newEpisode = testUtils.generateEpisode({ date: new Date("2024-01-01") });

  series.episodes = [oldEpisode];
  storage.writeSeries(series);

  series.episodes = [newEpisode];
  storage.writeSeries(series);

  const readSeries = storage.readSeries(series);
  assertEquals(readSeries?.episodes.length, 2);
  assertEquals(readSeries?.episodes.map((episode) => episode.id).sort(), [oldEpisode.id, newEpisode.id].sort());
});

Deno.test("writing a series renews lastFetchedAt", () => {
  const series = testUtils.generateSeries();
  series.lastFetchedAt = new Date("2020-01-01");
  storage.writeSeries(series);
  assertEquals(storage.readSeries(series)?.lastFetchedAt.getFullYear(), 2020);

  series.lastFetchedAt = new Date("2024-06-01");
  storage.writeSeries(series);
  assertEquals(storage.readSeries(series)?.lastFetchedAt.getFullYear(), 2024);
});

Deno.test("known byte sizes survive an update without byte sizes", () => {
  const series = testUtils.generateSeries();
  const episode = testUtils.generateEpisode({ bytes: 12345 });
  series.episodes = [episode];
  storage.writeSeries(series);

  series.episodes = [{ ...episode, bytes: null }];
  storage.writeSeries(series);

  assertEquals(storage.readSeries(series)?.episodes[0].bytes, 12345);
});

Deno.test("backlog state round-trips", () => {
  const series = testUtils.generateSeries();
  storage.writeSeries(series);

  storage.setBacklogState(series.id, "/radio/catalog/podcast/x/episodes?page=2", false);
  let readSeries = storage.readSeries(series);
  assertEquals(readSeries?.backlogComplete, false);
  assertEquals(readSeries?.backlogCursor, "/radio/catalog/podcast/x/episodes?page=2");

  storage.setBacklogState(series.id, null, true);
  readSeries = storage.readSeries(series);
  assertEquals(readSeries?.backlogComplete, true);
  assertEquals(readSeries?.backlogCursor, null);
});

Deno.test("addEpisodes and readEpisodeIds work", () => {
  const series = testUtils.generateSeries();
  series.episodes = [];
  storage.writeSeries(series);

  const episodes = [testUtils.generateEpisode(), testUtils.generateEpisode()];
  storage.addEpisodes(series.id, episodes);

  const ids = storage.readEpisodeIds(series.id);
  assertEquals(ids.size, 2);
  assertEquals(episodes.every((episode) => ids.has(episode.id)), true);
});

Deno.test("unknown series reads as null", () => {
  assertEquals(storage.readSeries({ id: "does-not-exist" }), null);
});

Deno.test("data version bumps on content writes only", () => {
  const series = testUtils.generateSeries();
  const before = storage.getDataVersion(series.id);

  storage.writeSeries(series);
  const afterWrite = storage.getDataVersion(series.id);
  assertEquals(afterWrite > before, true);

  storage.addEpisodes(series.id, [testUtils.generateEpisode()]);
  const afterAdd = storage.getDataVersion(series.id);
  assertEquals(afterAdd > afterWrite, true);

  storage.setBacklogState(series.id, null, true);
  assertEquals(storage.getDataVersion(series.id), afterAdd);
});

Deno.test("failed episodes are blocked until their retry window passes", () => {
  const seriesId = "backoff-test-" + crypto.randomUUID();
  storage.recordEpisodeFailure(seriesId, "ep-x");
  assertEquals(storage.readBlockedEpisodeIds(seriesId).has("ep-x"), true);
  assertEquals(storage.readBlockedEpisodeIds(seriesId).has("ep-y"), false);
});

Deno.test("garbage collection removes stale series and keeps fresh ones", () => {
  const stale = testUtils.generateSeries({ lastFetchedAt: new Date(Date.now() - 100 * 24 * 60 * 60 * 1000) });
  const fresh = testUtils.generateSeries({ lastFetchedAt: new Date() });
  storage.writeSeries(stale);
  storage.writeSeries(fresh);
  storage.recordEpisodeFailure(stale.id, "dead-ep");

  const deleted = storage.deleteStaleSeries(90 * 24 * 60 * 60 * 1000);
  assertEquals(deleted >= 1, true);
  assertEquals(storage.readSeries({ id: stale.id }), null);
  assertEquals(storage.readEpisodeIds(stale.id).size, 0);
  assertEquals(storage.readBlockedEpisodeIds(stale.id).size, 0);
  assertEquals(storage.readSeries({ id: fresh.id })?.id, fresh.id);
});

Deno.test("streamOnlySeriesIds flags all-HLS series only", () => {
  const streamOnly = testUtils.generateSeries();
  streamOnly.episodes = [
    testUtils.generateEpisode({ url: "https://cdn/x/muxed.m3u8?adap=audio" }),
    testUtils.generateEpisode({ url: "https://cdn/y/muxed.m3u8?adap=audio" }),
  ];
  const mixed = testUtils.generateSeries();
  mixed.episodes = [
    testUtils.generateEpisode({ url: "https://podkast.nrk.no/a.mp3" }),
    testUtils.generateEpisode({ url: "https://cdn/z/muxed.m3u8" }),
  ];
  storage.writeSeries(streamOnly);
  storage.writeSeries(mixed);

  const flagged = storage.streamOnlySeriesIds([streamOnly.id, mixed.id, "does-not-exist"]);
  assertEquals(flagged.has(streamOnly.id), true);
  assertEquals(flagged.has(mixed.id), false);
  assertEquals(flagged.has("does-not-exist"), false);
});
