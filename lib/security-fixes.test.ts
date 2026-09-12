import { assertEquals, assertExists } from "@std/assert";
import { tryLogin } from "./admin-auth.ts";
import { peekFeed, renderFeed } from "./feed-cache.ts";
import { caching } from "./caching.ts";
import { nrkRadio } from "./nrk/nrk.ts";
import { storage } from "./storage.ts";
import { testUtils } from "./test-utils.ts";

Deno.test("admin cookie keeps Secure behind an HTTPS-terminating proxy", async () => {
  Deno.env.set("ADMIN_TOKEN", "test-token-12345");
  try {
    const proxied = new Request("http://internal:8000/admin", {
      headers: { "x-forwarded-proto": "https" },
    });
    const headers = await tryLogin(proxied, "test-token-12345");
    assertExists(headers);
    assertEquals(headers.get("set-cookie")?.includes("Secure"), true);

    const local = new Request("http://localhost:8000/admin");
    const localHeaders = await tryLogin(local, "test-token-12345");
    assertExists(localHeaders);
    assertEquals(localHeaders.get("set-cookie")?.includes("Secure"), false);
  } finally {
    Deno.env.delete("ADMIN_TOKEN");
  }
});

Deno.test("stale render cached under a pre-write version self-heals", async () => {
  const series = testUtils.generateSeries();
  storage.writeSeries(series);

  // snapshot version, then a write lands (the race)
  const versionBefore = storage.getDataVersion(series.id);
  const staleCopy = storage.readSeries({ id: series.id })!;
  storage.addEpisodes(series.id, [testUtils.generateEpisode()]);

  // stale data rendered under the OLD snapshot
  await renderFeed(staleCopy, "https://kringkast.ing", versionBefore);

  // the current version no longer matches, so the stale entry is invisible
  const current = storage.getDataVersion(series.id);
  assertEquals(peekFeed(series.id, "https://kringkast.ing", current), null);

  // a fresh render under the current version includes the new episode
  const fresh = storage.readSeries({ id: series.id })!;
  const rendered = await renderFeed(fresh, "https://kringkast.ing", current);
  assertEquals(rendered.xml.includes(fresh.episodes[0].title), true);
});

Deno.test("fresh memoized feeds are served without full episode reads", async () => {
  const series = testUtils.generateSeries({ lastFetchedAt: new Date() });
  storage.writeSeries(series);

  const version = storage.getDataVersion(series.id);
  const full = storage.readSeries({ id: series.id })!;
  await renderFeed(full, "https://kringkast.ing", version);

  const readsBefore = storage.getFullReadCount();
  for (let poll = 0; poll < 5; poll++) {
    const memo = peekFeed(series.id, "https://kringkast.ing", storage.getDataVersion(series.id));
    assertExists(memo);
    assertEquals(caching.metaIsFresh(series.id), true);
  }
  assertEquals(storage.getFullReadCount(), readsBefore);
});

Deno.test("partial pagination during refresh reports sawAllPages=false", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = ((input: Request | URL | string) => {
    const url = String(input instanceof Request ? input.url : input);
    const { searchParams, pathname } = new URL(url);
    if (pathname.endsWith("/episodes") && searchParams.get("page") === "2") {
      return Promise.resolve(new Response("down", { status: 503 }));
    }
    if (pathname.endsWith("/episodes")) {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            _embedded: {
              episodes: [{
                id: "ny1",
                episodeId: "ny1",
                titles: { title: "Ny 1", subtitle: null },
                date: "2026-09-01T10:00:00Z",
                durationInSeconds: 100,
                _links: {},
              }],
            },
            _links: { next: { href: "/radio/catalog/podcast/delvis/episodes?page=2&pageSize=50" } },
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        ),
      );
    }
    if (pathname.startsWith("/playback/manifest/")) {
      return Promise.resolve(
        new Response(JSON.stringify({ playable: { assets: [{ url: "https://cdn.test/ny1.mp3" }] } }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );
    }
    return Promise.resolve(new Response(null, { status: 200, headers: { "content-length": "10" } }));
  }) as typeof fetch;

  try {
    const result = await nrkRadio.getNewEpisodes("delvis", "podcast", { skipEpisodeIds: new Set() });
    assertExists(result);
    assertEquals(result.episodes.length, 1);
    assertEquals(result.sawAllPages, false);
  } finally {
    globalThis.fetch = original;
  }
});
