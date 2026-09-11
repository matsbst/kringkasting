import { assertEquals, assertExists } from "@std/assert";
import { testUtils } from "./test-utils.ts";
import { forTestingOnly, rss } from "./rss.ts";

const ORIGIN = "https://nrss.example.com";

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

Deno.test("special characters are escaped", () => {
  const series = testUtils.generateSeries();
  series.title = `Ost & kjeks <3`;
  const feed = rss.assembleFeed(series, ORIGIN);
  assertEquals(feed.includes("Ost &amp; kjeks &lt;3"), true);
});
