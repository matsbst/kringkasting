import { assertEquals, assertExists } from "@std/assert";
import { storage } from "./storage.ts";
import { caching, forTestingOnly as cacheTesting } from "./caching.ts";
import { getChapters } from "./chapters.ts";
import { forTestingOnly as catalogTesting, getCatalogSize } from "./catalog.ts";
import { canRetry, clearRetry, deferRetry, parseRetryAfter, retryAt } from "./retry.ts";
import { get } from "./http.ts";
import { enqueueBacklogCrawl, forTestingOnly as backlogTesting } from "./backlog.ts";
import { nrkRadio } from "./nrk/nrk.ts";
import { testUtils } from "./test-utils.ts";

const DAY = 24 * 60 * 60_000;
const json = (body: unknown, status = 200, headers = {}) => new Response(JSON.stringify(body), { status, headers });
const episode = (id: string) => ({
  id,
  episodeId: id,
  titles: { title: id },
  date: "2020-01-01",
  durationInSeconds: 60,
  _links: {},
});

Deno.test("polling tiers cover active, dormant and empty shows", () => {
  for (const [days, hours] of [[0, 3], [2, 12], [30, 72], [180, 168]]) {
    assertEquals(cacheTesting.intervalHoursFor(new Date(Date.now() - days * DAY)), hours);
  }
  assertEquals(cacheTesting.intervalHoursFor(null), 3);
});

Deno.test("chapter lookups coalesce and persist both chapters and confirmed absence", async () => {
  const original = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (() => {
    calls++;
    return Promise.resolve(json({ indexPoints: [{ title: "Intro", startPoint: "PT10S" }] }));
  }) as typeof fetch;
  try {
    const results = await Promise.all(Array.from({ length: 8 }, () => getChapters("chapter-test", "one")));
    assertEquals(calls, 1);
    assertEquals(results[0].chapters, [{ title: "Intro", startTime: 10 }]);
    await getChapters("chapter-test", "one");
    assertEquals(calls, 1);
    assertExists(storage.readState("chapters:chapter-test:one"));

    globalThis.fetch = (() => {
      calls++;
      return Promise.resolve(json({}));
    }) as typeof fetch;
    assertEquals((await getChapters("chapter-test", "empty")).chapters, null);
    await getChapters("chapter-test", "empty");
    assertEquals(calls, 2);

    globalThis.fetch = (() => {
      calls++;
      return Promise.resolve(json({}, 404));
    }) as typeof fetch;
    assertEquals((await getChapters("chapter-test", "missing")).status, 404);
    await getChapters("chapter-test", "missing");
    assertEquals(calls, 4); // Both catalog families, only once.
  } finally {
    globalThis.fetch = original;
  }
});

Deno.test("chapter failures retain stale data and suppress immediate retries", async () => {
  storage.writeState("chapters:chapter-stale:one", {
    status: 200,
    chapters: [{ title: "Saved", startTime: 0 }],
    expiresAt: Date.now() - 1,
  });
  const original = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (() => {
    calls++;
    return Promise.resolve(json({}, 503));
  }) as typeof fetch;
  try {
    assertEquals((await getChapters("chapter-stale", "one")).chapters?.[0].title, "Saved");
    await getChapters("chapter-stale", "one");
    assertEquals(calls, 2);
    assertEquals((await getChapters("chapter-stale", "uncached")).status, 503);
    assertEquals(storage.readState("chapters:chapter-stale:uncached"), null);
  } finally {
    globalThis.fetch = original;
  }
});

Deno.test("weekly catalog restores from durable state and rejects partial replacement", async () => {
  const original = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (() => {
    calls++;
    return Promise.resolve(json({ series: [{ seriesId: "saved", title: "Saved" }], _links: {} }));
  }) as typeof fetch;
  try {
    await catalogTesting.loadCatalog();
    catalogTesting.resetMemory();
    assertEquals(getCatalogSize(), 1);
    assertEquals(calls, 1);
    const saved = storage.readState<{ expires: number }>("catalog")!;
    assertEquals(saved.expires > Date.now() + 6 * DAY, true);
    globalThis.fetch = ((input: Request | URL | string) =>
      String(input).includes("page=2") ? Promise.resolve(json({}, 503)) : Promise.resolve(
        json({
          series: [{ seriesId: "partial", title: "Partial" }],
          _links: { nextPage: { href: "/catalog?page=2" } },
        }),
      )) as typeof fetch;
    await catalogTesting.loadCatalog();
    assertEquals(storage.readState("catalog"), saved);
  } finally {
    globalThis.fetch = original;
  }
});

Deno.test("retry deadlines increase and Retry-After supports seconds and dates", async () => {
  const key = "policy-test";
  deferRetry(key);
  const first = retryAt(key);
  deferRetry(key);
  assertEquals(retryAt(key) > first, true);
  assertEquals(canRetry(key), false);
  clearRetry(key);
  assertEquals(canRetry(key), true);
  assertEquals(parseRetryAfter("120", 1000), 121000);
  assertEquals(parseRetryAfter("Thu, 01 Jan 1970 00:02:00 GMT", 1000), 120000);
  assertEquals(parseRetryAfter("invalid", 1000), 0);
  const original = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (() => {
    calls++;
    return Promise.resolve(json({}, 429, { "Retry-After": "3600" }));
  }) as typeof fetch;
  try {
    await get("https://retry-policy.test/first");
    await get("https://retry-policy.test/second");
    assertEquals(calls, 1);
    assertEquals(retryAt("origin:https://retry-policy.test") >= Date.now() + 3599_000, true);
  } finally {
    globalThis.fetch = original;
  }
});

Deno.test("partial refresh resumes with overlap without scanning the old archive", async () => {
  const id = "checkpoint-policy";
  const stored = testUtils.generateSeries({
    id,
    catalogKind: "podcast",
    lastFetchedAt: new Date(Date.now() - 10 * DAY),
    episodes: [testUtils.generateEpisode({ id: "boundary", date: new Date("2019-01-01") })],
  });
  storage.writeSeries(stored);
  storage.setBacklogState(id, null, true);
  const original = globalThis.fetch;
  let fail = true;
  const pages: number[] = [];
  globalThis.fetch = ((input: Request | URL | string, init?: RequestInit) => {
    const url = new URL(String(input));
    if (init?.method === "HEAD") return Promise.resolve(new Response(null));
    if (url.pathname.includes("/playback/")) {
      return Promise.resolve(json({ playable: { assets: [{ url: "https://checkpoint-cdn.test/file" }] } }));
    }
    const page = Number(url.searchParams.get("page") ?? 1);
    pages.push(page);
    if (page === 3 && fail) return Promise.resolve(json({}, 503));
    return Promise.resolve(
      json({
        _embedded: { episodes: [episode(page === 4 ? "boundary" : `new-${page}`)] },
        _links: { next: { href: `/radio/catalog/podcast/${id}/episodes?page=${page + 1}&pageSize=50` } },
      }),
    );
  }) as typeof fetch;
  try {
    await caching.getSeries({ id });
    assertEquals(pages, [1, 2, 3]);
    assertEquals(storage.readSeries({ id })?.backlogComplete, true);
    const progress = storage.readState<{ cursor: string }>(`refresh-progress:${id}`)!;
    assertEquals(progress.cursor.includes("page=2"), true);
    await caching.getSeries({ id });
    assertEquals(pages, [1, 2, 3]);
    clearRetry(`refresh:${id}`);
    clearRetry(`GET:https://psapi.nrk.no/radio/catalog/podcast/${id}/episodes?page=3&pageSize=50`);
    fail = false;
    await caching.getSeries({ id });
    assertEquals(pages, [1, 2, 3, 2, 3, 4]);
    assertEquals(storage.readState(`refresh-progress:${id}`), null);
    assertEquals(storage.readEpisodeIds(id).size, 4);
  } finally {
    globalThis.fetch = original;
  }
});

Deno.test("paused backlogs honor retry deadlines even on repeated feed requests", async () => {
  const id = "backlog-policy";
  storage.writeSeries(testUtils.generateSeries({ id, episodes: [], lastFetchedAt: new Date() }));
  const original = nrkRadio.getEpisodePage;
  let calls = 0;
  nrkRadio.getEpisodePage = () => {
    calls++;
    return Promise.resolve(null);
  };
  try {
    enqueueBacklogCrawl(id);
    await backlogTesting.waitForIdle();
    for (let i = 0; i < 5; i++) await caching.getSeries({ id });
    await backlogTesting.waitForIdle();
    assertEquals(calls, 1);
    clearRetry(`backlog:${id}`);
    assertEquals(caching.metaIsFresh(id), false); // Cached XML must not prevent a due resume.
    enqueueBacklogCrawl(id);
    await backlogTesting.waitForIdle();
    assertEquals(calls, 2);
  } finally {
    nrkRadio.getEpisodePage = original;
  }
});

Deno.test("requesting an old archive retains its saved episodes", async () => {
  const id = "retained-policy";
  storage.writeSeries(
    testUtils.generateSeries({
      id,
      lastFetchedAt: new Date(Date.now() - 400 * DAY),
      episodes: [testUtils.generateEpisode({ id: "saved" })],
    }),
  );
  storage.setBacklogState(id, null, true);
  deferRetry(`refresh:${id}`);
  const result = await caching.getSeries({ id });
  assertEquals(result?.episodes[0].id, "saved");
  assertEquals(storage.hasSeries(id), true);
});

Deno.test("cache and retry state survive separate processes", async () => {
  const directory = await Deno.makeTempDir();
  const moduleUrl = new URL("./storage.ts", import.meta.url).href;
  const run = async (code: string) => {
    const result = await new Deno.Command(Deno.execPath(), {
      args: ["eval", `import { storage } from ${JSON.stringify(moduleUrl)}; ${code}`],
      env: { KRINGKASTING_DB_PATH: `${directory}/cache.sqlite3` },
      stdout: "piped",
      stderr: "piped",
    }).output();
    assertEquals(result.code, 0, new TextDecoder().decode(result.stderr));
    return new TextDecoder().decode(result.stdout).trim();
  };
  try {
    await run(
      'storage.writeState("catalog", {entries: ["saved"]}); storage.writeState("retry:work", {nextRetryAt: 123});',
    );
    assertEquals(
      await run('console.log(JSON.stringify([storage.readState("catalog"), storage.readState("retry:work")]));'),
      '[{"entries":["saved"]},{"nextRetryAt":123}]',
    );
  } finally {
    await Deno.remove(directory, { recursive: true });
  }
});
