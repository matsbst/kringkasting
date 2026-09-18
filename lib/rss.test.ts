import { assertEquals, assertExists } from "@std/assert";
import { testUtils } from "./test-utils.ts";
import { forTestingOnly, rss } from "./rss.ts";

const ORIGIN = "https://kringkast.ing";

// NOTE: Could probably be expanded upon.
Deno.test("generate tag for episode", () => {
  const episode = testUtils.generateEpisode();
  const tag = forTestingOnly.assembleEpisode(episode, "someId", ORIGIN);
  assertExists(tag);
});

Deno.test("generated rss contains the series title", () => {
  const series = testUtils.generateSeries();
  const feed = rss.assembleFeed(series, ORIGIN);
  assertExists(feed);
  assertEquals(feed.includes(series.title), true);
});

Deno.test("generated rss contains all episode titles", () => {
  const series = testUtils.generateSeries();
  const feed = rss.assembleFeed(series, ORIGIN);
  assertExists(feed);
  series.episodes.forEach((episode) => {
    assertEquals(feed.includes(episode.title), true);
  });
});

Deno.test("chapter URLs point at the given origin", () => {
  const series = testUtils.generateSeries();
  const feed = rss.assembleFeed(series, ORIGIN);
  assertEquals(feed.includes(`${ORIGIN}/api/feeds/${series.id}/`), true);
});

Deno.test("season feeds link to themselves but chapters resolve via the parent series", () => {
  const series = testUtils.generateSeries({ id: "radiodokumentaren/sesong/mannen-som-forsvant" });
  series.episodes = [testUtils.generateEpisode({ id: "ep1" })];
  const feed = rss.assembleFeed(series, ORIGIN);
  // atom:link rel=self must be the season feed's own URL
  assertEquals(feed.includes(`${ORIGIN}/api/feeds/radiodokumentaren/sesong/mannen-som-forsvant`), true);
  // chapters live under the parent seriesId (that's what NRK's catalog resolves)
  assertEquals(feed.includes(`${ORIGIN}/api/feeds/radiodokumentaren/ep1/chapters`), true);
});

Deno.test("special characters are escaped", () => {
  const series = testUtils.generateSeries();
  series.title = `Ost & kjeks <3`;
  const feed = rss.assembleFeed(series, ORIGIN);
  assertEquals(feed.includes("Ost &amp; kjeks &lt;3"), true);
});

Deno.test("enclosures use the registered audio/mpeg MIME type and unknown length", () => {
  const series = testUtils.generateSeries();
  series.episodes = [testUtils.generateEpisode()];
  const feed = rss.assembleFeed(series, ORIGIN);
  assertEquals(feed.includes(`type="audio/mpeg"`), true);
  assertEquals(feed.includes(`type="audio/mpeg3"`), false);
  assertEquals(feed.includes(`length="0"`), true);
});

Deno.test("HLS stream enclosures are typed as HLS, not audio/mpeg", () => {
  const series = testUtils.generateSeries();
  series.episodes = [
    testUtils.generateEpisode({ url: "https://nrk-od-world-15.akamaized.net/x/muxed.m3u8?adap=audio" }),
  ];
  const feed = rss.assembleFeed(series, ORIGIN);
  assertEquals(feed.includes(`type="application/vnd.apple.mpegurl"`), true);
});

Deno.test("progressive MP3 enclosures stay audio/mpeg", () => {
  const series = testUtils.generateSeries();
  series.episodes = [testUtils.generateEpisode({ url: "https://podkast.nrk.no/fil/x_ID192MP3.mp3" })];
  const feed = rss.assembleFeed(series, ORIGIN);
  assertEquals(feed.includes(`type="audio/mpeg"`), true);
  assertEquals(feed.includes("mpegurl"), false);
});

Deno.test("mixed shows drop HLS episodes; stream-only shows keep them", () => {
  const mp3 = () => testUtils.generateEpisode({ url: "https://podkast.nrk.no/fil/x_ID192MP3.mp3" });
  const hls = () => testUtils.generateEpisode({ url: "https://cdn.akamaized.net/x/muxed.m3u8?adap=audio" });

  const mixed = testUtils.generateSeries();
  mixed.episodes = [mp3(), hls(), mp3()];
  const mixedFeed = rss.assembleFeed(mixed, ORIGIN);
  assertEquals(mixedFeed.includes("m3u8"), false); // HLS dropped
  assertEquals((mixedFeed.match(/<item>/g) || []).length, 2);

  const streamOnly = testUtils.generateSeries();
  streamOnly.episodes = [hls(), hls()];
  const streamFeed = rss.assembleFeed(streamOnly, ORIGIN);
  assertEquals((streamFeed.match(/<item>/g) || []).length, 2); // kept
  assertEquals(streamFeed.includes("application/vnd.apple.mpegurl"), true);
});
