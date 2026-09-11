import { assertEquals, assertExists } from "@std/assert";
import { forTestingOnly, nrkRadio } from "./nrk.ts";
import { caching } from "../caching.ts";
import { storage } from "../storage.ts";

/**
 * Offline tests against a stubbed NRK API, so CI is deterministic.
 * The live-API integration tests live in tests_live/ (deno task test:live).
 */

type Stub = {
  requests: string[];
  restore: () => void;
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function episodeItem(id: string) {
  return {
    id,
    episodeId: id,
    titles: { title: `Episode ${id}`, subtitle: `Om ${id}` },
    date: "2024-06-01T10:00:00Z",
    durationInSeconds: 1800,
    _links: { share: { href: `https://radio.nrk.no/del/${id}` } },
  };
}

/**
 * Fake NRK: series "testserie" with episodes ep1/ep2 plus
 * "gone" (manifest 200 but non-playable) and "flaky" (manifest 503).
 * A second page exists for pagination tests.
 */
function stubNrk(): Stub {
  const original = globalThis.fetch;
  const requests: string[] = [];

  globalThis.fetch = ((input: Request | URL | string, init?: RequestInit) => {
    const url = String(input instanceof Request ? input.url : input);
    const method = init?.method ?? (input instanceof Request ? input.method : "GET");
    requests.push(`${method} ${url}`);

    if (method === "HEAD" && url.startsWith("https://cdn.test/")) {
      return Promise.resolve(new Response(null, { status: 200, headers: { "content-length": "12345" } }));
    }

    const { pathname, searchParams } = new URL(url);

    if (pathname === "/radio/catalog/podcast/testserie") {
      return Promise.resolve(jsonResponse({
        type: "podcast",
        seriesType: "standard",
        _links: {},
        series: {
          id: "testserie",
          titles: { title: "Testserie", subtitle: "En testserie" },
          squareImage: [{ url: "https://gfx.test/img", width: 300 }],
        },
      }));
    }

    if (pathname === "/radio/catalog/podcast/testserie/episodes") {
      if (searchParams.get("page") === "2") {
        return Promise.resolve(jsonResponse({
          _embedded: { episodes: [episodeItem("ep3")] },
          _links: {},
        }));
      }
      const withPaging = searchParams.has("pageSize");
      return Promise.resolve(jsonResponse({
        _embedded: { episodes: [episodeItem("ep1"), episodeItem("ep2"), episodeItem("gone"), episodeItem("flaky")] },
        _links: withPaging ? { next: { href: "/radio/catalog/podcast/testserie/episodes?page=2&pageSize=50" } } : {},
      }));
    }

    if (pathname.startsWith("/playback/manifest/podcast/")) {
      const id = pathname.split("/").at(-1);
      if (id === "gone") {
        return Promise.resolve(jsonResponse({ playable: null, nonPlayable: { reason: "expired" } }));
      }
      if (id === "flaky") {
        return Promise.resolve(jsonResponse({ message: "unavailable" }, 503));
      }
      return Promise.resolve(jsonResponse({ playable: { assets: [{ url: `https://cdn.test/${id}.mp3` }] } }));
    }

    if (pathname === "/radio/search/search") {
      return Promise.resolve(jsonResponse({
        results: {
          series: {
            results: [{
              seriesId: "testserie",
              title: "Testserie",
              images: [{ uri: "https://gfx.test/wide", width: 600 }],
              images_1_1: [{ uri: "https://gfx.test/square", width: 400 }],
            }],
          },
        },
      }));
    }

    return Promise.resolve(jsonResponse({ message: "not found" }, 404));
  }) as typeof fetch;

  return {
    requests,
    restore: () => {
      globalThis.fetch = original;
    },
  };
}

Deno.test("getSeries parses episodes with byte sizes and drops unavailable ones", async () => {
  const stub = stubNrk();
  try {
    const failures: string[] = [];
    const result = await nrkRadio.getSeries("testserie", { onEpisodeFailure: (id) => failures.push(id) });
    assertExists(result);
    if (result === "error") throw new Error("unexpected upstream error");
    const series = result;
    assertEquals(series.title, "Testserie");
    assertEquals(series.episodes.map((episode) => episode.id).sort(), ["ep1", "ep2"]);
    assertEquals(series.episodes[0].bytes, 12345);
    // "gone" is a definitive failure; "flaky" (503) is transient and not reported
    assertEquals(failures, ["gone"]);
  } finally {
    stub.restore();
  }
});

Deno.test("getSeries skips episodes in the skip set", async () => {
  const stub = stubNrk();
  try {
    const result = await nrkRadio.getSeries("testserie", { skipEpisodeIds: new Set(["ep1", "gone", "flaky"]) });
    assertExists(result);
    if (result === "error") throw new Error("unexpected upstream error");
    const series = result;
    assertEquals(series.episodes.map((episode) => episode.id), ["ep2"]);
    const manifestCalls = stub.requests.filter((line) => line.includes("/playback/manifest/"));
    assertEquals(manifestCalls.length, 1);
  } finally {
    stub.restore();
  }
});

Deno.test("getEpisodePage follows pagination and reports failures", async () => {
  const stub = stubNrk();
  try {
    const failures: string[] = [];
    const first = await nrkRadio.getEpisodePage("testserie", null, new Set(["ep1"]), (id) => failures.push(id));
    assertExists(first);
    assertEquals(first.episodes.map((episode) => episode.id), ["ep2"]);
    assertEquals(failures, ["gone"]);
    assertExists(first.nextHref);

    const second = await nrkRadio.getEpisodePage("testserie", first.nextHref, new Set());
    assertExists(second);
    assertEquals(second.episodes.map((episode) => episode.id), ["ep3"]);
    assertEquals(second.nextHref, null);
  } finally {
    stub.restore();
  }
});

Deno.test("search encodes the query and parses results", async () => {
  const stub = stubNrk();
  try {
    const result = await nrkRadio.search("berrum & beyer offline");
    assertExists(result);
    assertEquals(result[0].seriesId, "testserie");
    const searchCall = stub.requests.find((line) => line.includes("/radio/search/search"));
    assertExists(searchCall);
    assertEquals(searchCall.includes("berrum%20%26%20beyer%20offline"), true);
  } finally {
    stub.restore();
  }
});

Deno.test("caching stores fetched series and blocks failed episodes from refetching", async () => {
  const stub = stubNrk();
  try {
    const series = await caching.getSeries({ id: "testserie" });
    assertExists(series);
    assertEquals(series.episodes.length, 2);

    // the non-playable episode is now under backoff
    const blocked = storage.readBlockedEpisodeIds("testserie");
    assertEquals(blocked.has("gone"), true);

    // catalog kind was learned during the initial fetch
    assertEquals(storage.readSeries({ id: "testserie" })?.catalogKind, "podcast");

    // age the series past even the dormant-show refresh window (24h+jitter)
    const stored = storage.readSeries({ id: "testserie" })!;
    stored.lastFetchedAt = new Date(Date.now() - 48 * 60 * 60 * 1000);
    storage.writeSeries(stored);

    stub.requests.length = 0;
    const refreshed = await caching.getSeries({ id: "testserie" });
    assertExists(refreshed);

    // blocked episode's manifest is not re-requested during refresh
    const goneRefetches = stub.requests.filter((line) => line.includes("/playback/manifest/podcast/gone"));
    assertEquals(goneRefetches.length, 0);
    // cheap refresh: episode listing only — no metadata request, no
    // wrong-catalog fallback
    const metadataFetches = stub.requests.filter((line) => line.endsWith("/radio/catalog/podcast/testserie"));
    assertEquals(metadataFetches.length, 0);
    const fallbackFetches = stub.requests.filter((line) => line.includes("/radio/catalog/series/"));
    assertEquals(fallbackFetches.length, 0);
    // freshness renewed
    assertEquals(refreshed.lastFetchedAt.getTime() > Date.now() - 60_000, true);
  } finally {
    stub.restore();
  }
});

Deno.test("mapConcurrent preserves order and respects the concurrency limit", async () => {
  const limit = 3;
  let inFlight = 0;
  let maxInFlight = 0;
  const items = Array.from({ length: 20 }, (_, index) => index);

  const results = await forTestingOnly.mapConcurrent(items, limit, async (item) => {
    inFlight++;
    maxInFlight = Math.max(maxInFlight, inFlight);
    await new Promise((resolve) => setTimeout(resolve, 5));
    inFlight--;
    return item * 2;
  });

  assertEquals(results, items.map((item) => item * 2));
  assertEquals(maxInFlight <= limit, true, `max in flight was ${maxInFlight}`);
});

Deno.test("unknown series is not-found; upstream outage is a distinct error", async () => {
  const stub = stubNrk();
  try {
    assertEquals(await nrkRadio.getSeries("finnes-ikke"), null);
  } finally {
    stub.restore();
  }

  // an all-503 upstream must surface as "error", never as not-found
  const original = globalThis.fetch;
  globalThis.fetch = (() => Promise.resolve(new Response("down", { status: 503 }))) as typeof fetch;
  try {
    assertEquals(await nrkRadio.getSeries("testserie"), "error");
  } finally {
    globalThis.fetch = original;
  }
});

Deno.test("transient manifest failures are counted so the crawler can retry the page", async () => {
  const stub = stubNrk();
  try {
    const page = await nrkRadio.getEpisodePage("testserie", null, new Set(["ep1", "ep2", "gone"]));
    assertExists(page);
    // "flaky" (503) is the only unresolved candidate on the page
    assertEquals(page.transientFailures, 1);
  } finally {
    stub.restore();
  }
});

Deno.test("search returns [] for no matches and null for outages", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = (() =>
    Promise.resolve(
      new Response(JSON.stringify({ results: {} }), { status: 200, headers: { "content-type": "application/json" } }),
    )) as typeof fetch;
  try {
    assertEquals(await nrkRadio.search("ingen treff her"), []);
  } finally {
    globalThis.fetch = original;
  }

  globalThis.fetch = (() => Promise.resolve(new Response("down", { status: 503 }))) as typeof fetch;
  try {
    assertEquals(await nrkRadio.search("nede nå"), null);
  } finally {
    globalThis.fetch = original;
  }
});
