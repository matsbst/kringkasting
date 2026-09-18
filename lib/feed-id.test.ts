import { assertEquals } from "@std/assert";
import { isValidFeedId, parseFeedId, seasonFeedId } from "./feed-id.ts";
import { feedIdOf, toSeriesSummary } from "./series-summary.ts";
import type { SearchResult } from "./nrk/nrk.ts";

Deno.test("seasonFeedId mirrors NRK's URL structure", () => {
  assertEquals(
    seasonFeedId("radiodokumentaren", "mannen-som-forsvant"),
    "radiodokumentaren/sesong/mannen-som-forsvant",
  );
});

Deno.test("parseFeedId splits season feeds and passes plain ids through", () => {
  assertEquals(parseFeedId("hele-historien"), { seriesId: "hele-historien", seasonId: null });
  assertEquals(parseFeedId("radiodokumentaren/sesong/mannen-som-forsvant"), {
    seriesId: "radiodokumentaren",
    seasonId: "mannen-som-forsvant",
  });
});

Deno.test("isValidFeedId accepts both feed shapes and rejects garbage", () => {
  assertEquals(isValidFeedId("hele-historien"), true);
  assertEquals(isValidFeedId("radiodokumentaren/sesong/arsenikkmysteriet-"), true);
  assertEquals(isValidFeedId("a/sesong/"), false);
  assertEquals(isValidFeedId("a/b"), false);
  assertEquals(isValidFeedId("../etc/passwd"), false);
  assertEquals(isValidFeedId("a/sesong/b/sesong/c"), false);
});

Deno.test("customSeason search hits become season summaries with a season feed id", () => {
  const hit = {
    id: "hash",
    seriesId: "radiodokumentaren",
    seasonId: "mannen-som-forsvant",
    type: "customSeason",
    title: "Mannen som forsvant",
    images: [{ uri: "https://gfx.test/img", width: 300 }],
  } as SearchResult;
  const summary = toSeriesSummary(hit);
  assertEquals(summary.seasonId, "mannen-som-forsvant");
  assertEquals(feedIdOf(summary), "radiodokumentaren/sesong/mannen-som-forsvant");

  // ordinary podcasts keep their plain id
  const plain = toSeriesSummary({ ...hit, type: "podcast", seasonId: undefined } as SearchResult);
  assertEquals(plain.seasonId, undefined);
  assertEquals(feedIdOf(plain), "radiodokumentaren");
});
