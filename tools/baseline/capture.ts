/**
 * Captures the Deno app's HTTP behaviour as golden files for the Go
 * rewrite (docs/go-migration.md, step 1).
 *
 *   deno task build
 *   deno run -A tools/baseline/capture.ts record   # hits NRK for everything, saves fixtures
 *   deno run -A tools/baseline/capture.ts fill     # replays, fetching only missing fixtures
 *   deno run -A tools/baseline/capture.ts replay   # offline: writes testdata/baseline/http
 *
 * Replay is deterministic; running it twice must produce no diff.
 */

const mode = ["record", "fill"].includes(Deno.args[0]) ? Deno.args[0] : "replay";
const FIXTURE_DIR = "testdata/nrk";
const GOLDEN_DIR = "testdata/baseline/http";
const DB_SNAPSHOT = "testdata/baseline/deno-created.sqlite3";
const ORIGIN = "https://kringkasting.example";
const ADMIN_TOKEN = "baseline-admin-token";
const PORT = 8765;
const BASE = `http://127.0.0.1:${PORT}`;

/** feeds covering the cases in the plan; see testdata/README.md */
const FEEDS = [
  "supertroett", // ordinary podcast, 6 episodes
  "doemt", // sequential podcast, 7 episodes
  "musikkhistorier", // umbrella podcast with customSeasons
  "musikkhistorier/sesong/miles-davis--bak-mytene", // season feed
  "musikkhistorier/sesong/nick-cave--moerkets-lysmester", // season feed
  "radioteatret-nrk-p1pluss", // radio series, HLS only, 2 pages
];

/** headers whose values depend on the wall clock */
const DYNAMIC_HEADERS = new Set(["date", "expires", "last-modified"]);

type Server = { process: Deno.ChildProcess; dbPath: string };

async function startServer(env: Record<string, string>, existingDb?: string): Promise<Server> {
  let dbPath = existingDb;
  if (!dbPath) {
    dbPath = await Deno.makeTempFile({ prefix: "baseline-", suffix: ".sqlite3" });
    await Deno.remove(dbPath);
  }
  const process = new Deno.Command(Deno.execPath(), {
    args: ["run", "-A", "tools/baseline/serve.ts"],
    env: {
      NRK_FIXTURES: mode,
      FIXTURE_DIR,
      PORT: String(PORT),
      KRINGKASTING_DB_PATH: dbPath,
      ...env,
    },
    clearEnv: true,
    stdout: "inherit",
    stderr: "inherit",
  }).spawn();
  for (let attempt = 0; attempt < 100; attempt++) {
    try {
      await (await fetch(`${BASE}/personvern`)).body?.cancel();
      return { process, dbPath };
    } catch {
      await sleep(100);
    }
  }
  throw new Error("server did not start");
}

async function stopServer(server: Server) {
  server.process.kill("SIGTERM");
  await server.process.status;
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** request a feed until the backlog crawl is done and the XML stops changing */
async function warmFeed(id: string) {
  let previous = "";
  let stableRounds = 0;
  for (let attempt = 0; attempt < 120 && stableRounds < 3; attempt++) {
    const response = await fetch(`${BASE}/api/feeds/${id}`);
    const body = await response.text();
    stableRounds = response.ok && body === previous ? stableRounds + 1 : 0;
    previous = body;
    await sleep(2_000);
  }
  if (stableRounds < 3) throw new Error(`feed ${id} never stabilised`);
}

type Capture = {
  name: string;
  method?: string;
  path: string;
  headers?: Record<string, string>;
  body?: string;
  /** keep only the status line and headers (body is too dynamic) */
  headersOnly?: boolean;
};

async function capture(c: Capture): Promise<Response> {
  const method = c.method ?? "GET";
  const response = await fetch(`${BASE}${c.path}`, {
    method,
    headers: c.headers,
    body: c.body,
    redirect: "manual",
  });
  const body = method === "HEAD" ? "" : normalizeBody(await response.text());

  const lines = [`${method} ${c.path}`];
  for (const [name, value] of Object.entries(c.headers ?? {})) lines.push(`${name}: ${value}`);
  if (c.body) lines.push("", c.body);
  lines.push("", `HTTP ${response.status}`);
  const headers = [...response.headers.entries()]
    .map(([name, value]) => `${name}: ${DYNAMIC_HEADERS.has(name) ? "<dynamic>" : value}`)
    .sort();
  lines.push(...headers, "");
  if (!c.headersOnly) lines.push(body);

  await Deno.writeTextFile(`${GOLDEN_DIR}/${c.name}.txt`, lines.join("\n"));
  return response;
}

/** Fresh adds a random CSP nonce to inline scripts on every request */
function normalizeBody(body: string): string {
  return body.replace(/nonce="[0-9a-f]+"/g, 'nonce="<dynamic>"');
}

/** the homepage loads NRK's catalogue in the background; wait until it settles */
async function warmHome() {
  let previous = "";
  let stableRounds = 0;
  for (let attempt = 0; attempt < 60 && stableRounds < 3; attempt++) {
    const body = normalizeBody(await (await fetch(`${BASE}/`)).text());
    stableRounds = body === previous ? stableRounds + 1 : 0;
    previous = body;
    await sleep(2_000);
  }
  if (stableRounds < 3) throw new Error("homepage never stabilised");
}

async function captureFeeds() {
  for (const id of FEEDS) {
    const slug = id.replaceAll("/", "_");
    const first = await capture({ name: `feed_${slug}`, path: `/api/feeds/${id}` });
    const etag = first.headers.get("etag")!;
    const lastModified = first.headers.get("last-modified")!;
    await capture({ name: `feed_${slug}_304_etag`, path: `/api/feeds/${id}`, headers: { "If-None-Match": etag } });
    await capture({
      name: `feed_${slug}_304_ims`,
      path: `/api/feeds/${id}`,
      headers: { "If-Modified-Since": lastModified },
    });
    // If-None-Match wins over If-Modified-Since (RFC 9110): stale etag + matching date = 200
    await capture({
      name: `feed_${slug}_etag_precedence`,
      path: `/api/feeds/${id}`,
      headers: { "If-None-Match": '"stale"', "If-Modified-Since": lastModified },
      headersOnly: true,
    });
  }
  await capture({ name: "feed_head", method: "HEAD", path: "/api/feeds/supertroett" });
  await capture({ name: "feed_trailing_slash", path: "/api/feeds/supertroett/" });
  await capture({ name: "feed_post", method: "POST", path: "/api/feeds/supertroett" });
  await capture({ name: "feed_invalid_id", path: "/api/feeds/..%2Fetc" });
  await capture({ name: "feed_unknown", path: "/api/feeds/finnes-ikke-baseline" });
  await capture({ name: "season_unknown", path: "/api/feeds/musikkhistorier/sesong/finnes-ikke-baseline" });
}

async function captureChapters() {
  // today's feeds link chapters by NRK's hash `id`, which NRK's episode
  // endpoint answers with 404 (a known bug, kept here as current behaviour)
  for (const feed of ["supertroett", "musikkhistorier"]) {
    const xml = await Deno.readTextFile(`${GOLDEN_DIR}/feed_${feed}.txt`);
    const url = xml.match(/podcast:chapters url="([^"]+)"/)?.[1];
    if (url) await capture({ name: `chapters_${feed}_feed_link`, path: new URL(url).pathname });
  }
  // the same lookup by NRK's real `episodeId` succeeds
  await capture({
    name: "chapters_supertroett_episode_id",
    path: "/api/feeds/supertroett/l_dc4c5213-d03c-4491-8c52-13d03cc491a1/chapters",
  });
  await capture({
    name: "chapters_musikkhistorier_episode_id",
    path: "/api/feeds/musikkhistorier/l_6f0c81a3-4e1c-4cee-8c81-a34e1c0cee7c/chapters",
  });
  await capture({ name: "chapters_unknown", path: "/api/feeds/supertroett/finnes-ikke/chapters" });
  await capture({ name: "chapters_invalid_id", path: "/api/feeds/supertroett/..%2F..%2Fx/chapters" });
}

async function capturePages() {
  await warmHome();
  await capture({ name: "page_home", path: "/" });
  await capture({ name: "page_home_search", path: "/?query=musikk" });
  await capture({ name: "page_home_search_none", path: "/?query=zzqqxxnotfound" });
  await capture({ name: "page_personvern", path: "/personvern" });
  await capture({ name: "page_sitemap", path: "/sitemap.xml" });
  await capture({ name: "page_not_found", path: "/finnes-ikke" });
  await capture({ name: "api_search", path: "/api/search?q=musikk" });
  await capture({ name: "api_search_empty", path: "/api/search?q=" });
  await capture({ name: "api_search_post", method: "POST", path: "/api/search?q=musikk" });
}

async function captureStatic() {
  for (const entry of Deno.readDirSync("static")) {
    if (entry.isFile && !entry.name.startsWith(".")) {
      await capture({ name: `static_${entry.name}`, path: `/${entry.name}`, headersOnly: true });
    }
  }
  for (const entry of Deno.readDirSync("static/fonts")) {
    await capture({ name: `static_fonts_${entry.name}`, path: `/fonts/${entry.name}`, headersOnly: true });
    break; // one font is enough for the header rules
  }
}

async function captureAdmin() {
  const form = (fields: Record<string, string>) => new URLSearchParams(fields).toString();
  const formHeaders = { "Content-Type": "application/x-www-form-urlencoded" };
  const cookie = `kringkasting_admin=${encodeURIComponent(ADMIN_TOKEN)}`;

  await capture({ name: "admin_login_page", path: "/admin" });
  await capture({
    name: "admin_login_wrong",
    method: "POST",
    path: "/admin",
    headers: formHeaders,
    body: form({ token: "wrong-token-123" }),
  });
  await capture({
    name: "admin_login_ok",
    method: "POST",
    path: "/admin",
    headers: formHeaders,
    body: form({ token: ADMIN_TOKEN }),
  });
  await capture({
    name: "admin_login_ok_forwarded_https",
    method: "POST",
    path: "/admin",
    headers: { ...formHeaders, "X-Forwarded-Proto": "https" },
    body: form({ token: ADMIN_TOKEN }),
  });
  await capture({ name: "admin_dashboard", path: "/admin", headers: { Cookie: cookie }, headersOnly: true });
  await capture({
    name: "admin_action_unauthenticated",
    method: "POST",
    path: "/admin",
    headers: formHeaders,
    body: form({ action: "refresh", seriesId: "supertroett" }),
  });
  await capture({
    name: "admin_action_refresh",
    method: "POST",
    path: "/admin",
    headers: { ...formHeaders, Cookie: cookie },
    body: form({ action: "refresh", seriesId: "supertroett" }),
  });
  await capture({
    name: "admin_action_unknown",
    method: "POST",
    path: "/admin",
    headers: { ...formHeaders, Cookie: cookie },
    body: form({ action: "explode", seriesId: "supertroett" }),
  });
}

const SCENARIO_DIR = "testdata/scenarios";
const DAY_MS = 24 * 60 * 60 * 1000;

/** write an override for one recorded NRK response */
async function overrideFixture(scenario: string, url: string, status: number, body: string) {
  const { fixtureKey } = await import("./harness.ts");
  const dir = `${SCENARIO_DIR}/${scenario}`;
  await Deno.mkdir(dir, { recursive: true });
  const fixture = { method: "GET", url, status, headers: { "content-type": "application/json" }, body };
  await Deno.writeTextFile(`${dir}/${fixtureKey("GET", url)}.json`, JSON.stringify(fixture, null, 2) + "\n");
}

/** the durable state Go must reproduce, without wall-clock timestamps */
async function dumpState(dbPath: string, name: string) {
  const { DatabaseSync } = await import("node:sqlite");
  const db = new DatabaseSync(dbPath);
  const lines: string[] = [];
  const rows = (sql: string) => db.prepare(sql).all() as Record<string, unknown>[];
  lines.push("# series: id | backlog_complete | backlog_cursor | catalog_kind | is_umbrella");
  for (const r of rows("SELECT * FROM series ORDER BY id")) {
    lines.push([r.id, r.backlog_complete, r.backlog_cursor, r.catalog_kind, r.is_umbrella].join(" | "));
  }
  lines.push("# episodes: series_id | id | url | date | duration_seconds | bytes");
  for (const r of rows("SELECT * FROM episodes ORDER BY series_id, id")) {
    lines.push([r.series_id, r.id, r.url, r.date, r.duration_seconds, r.bytes].join(" | "));
  }
  lines.push("# failed_episodes: series_id | id | attempts");
  for (const r of rows("SELECT * FROM failed_episodes ORDER BY series_id, id")) {
    lines.push([r.series_id, r.id, r.attempts].join(" | "));
  }
  lines.push("# app_state keys");
  for (const r of rows("SELECT key FROM app_state ORDER BY key")) lines.push(String(r.key));
  db.close();
  await Deno.writeTextFile(`${GOLDEN_DIR}/${name}.txt`, lines.join("\n") + "\n");
}

async function captureFailedEpisode() {
  // NRK has no playable manifest for one episode (404): it is skipped,
  // recorded in failed_episodes and left out of the feed
  await overrideFixture(
    "failed-episode",
    "https://psapi.nrk.no/playback/manifest/podcast/l_dc4c5213-d03c-4491-8c52-13d03cc491a1",
    404,
    '"Not found"',
  );
  const server = await startServer({ APP_ORIGIN: ORIGIN, FIXTURE_OVERLAY: `${SCENARIO_DIR}/failed-episode` });
  try {
    await warmFeed("supertroett");
    await capture({ name: "scenario_failed_episode_feed", path: "/api/feeds/supertroett" });
  } finally {
    await stopServer(server);
  }
  await dumpState(server.dbPath, "scenario_failed_episode_state");
}

async function captureInterruptedCrawl() {
  // page 2 of the archive fails (503): the crawl pauses with its cursor
  // saved; after a restart two days later a new request resumes it
  await overrideFixture(
    "interrupted-crawl",
    "https://psapi.nrk.no/radio/catalog/series/radioteatret-nrk-p1pluss/episodes?page=2&pageSize=50&sort=desc",
    503,
    '"Service unavailable"',
  );
  const id = "radioteatret-nrk-p1pluss";
  const first = await startServer({ APP_ORIGIN: ORIGIN, FIXTURE_OVERLAY: `${SCENARIO_DIR}/interrupted-crawl` });
  try {
    await warmFeed(id);
    await capture({ name: "scenario_interrupted_crawl_paused_feed", path: `/api/feeds/${id}` });
  } finally {
    await stopServer(first);
  }
  await dumpState(first.dbPath, "scenario_interrupted_crawl_paused_state");

  const second = await startServer({ APP_ORIGIN: ORIGIN, CLOCK_OFFSET_MS: String(2 * DAY_MS) }, first.dbPath);
  try {
    await warmFeed(id);
    await capture({ name: "scenario_interrupted_crawl_resumed_feed", path: `/api/feeds/${id}` });
  } finally {
    await stopServer(second);
  }
  await dumpState(second.dbPath, "scenario_interrupted_crawl_resumed_state");
}

async function snapshotDatabase(dbPath: string) {
  // the server is stopped, so the WAL has been checkpointed or is safe to copy alongside
  const { DatabaseSync } = await import("node:sqlite");
  const db = new DatabaseSync(dbPath);
  db.exec("PRAGMA wal_checkpoint(TRUNCATE)");
  db.exec(`VACUUM INTO '${DB_SNAPSHOT}'`);
  db.close();
}

await Deno.mkdir(FIXTURE_DIR, { recursive: true });
await Deno.mkdir(GOLDEN_DIR, { recursive: true });
try {
  await Deno.remove(DB_SNAPSHOT);
} catch { /* first run */ }

// run 1: admin enabled, fixed public origin
const main = await startServer({ APP_ORIGIN: ORIGIN, ADMIN_TOKEN });
try {
  for (const id of FEEDS) {
    console.log(`warming ${id}`);
    await warmFeed(id);
  }
  await captureFeeds();
  await captureChapters();
  await capturePages();
  await captureStatic();
  await captureAdmin();
} finally {
  await stopServer(main);
}
await snapshotDatabase(main.dbPath);

// run 2: admin disabled, no configured origin (Host and forwarding headers decide)
const bare = await startServer({});
try {
  await capture({ name: "admin_disabled", path: "/admin" });
  await capture({ name: "admin_disabled_post", method: "POST", path: "/admin" });
  await warmFeed("supertroett");
  await capture({ name: "origin_from_host", path: "/api/feeds/supertroett", headersOnly: false });
  await capture({
    name: "origin_forwarded",
    path: "/api/feeds/supertroett",
    headers: { "X-Forwarded-Proto": "https", "X-Forwarded-Host": "proxy.example" },
  });
} finally {
  await stopServer(bare);
}

// scenarios: NRK failures that can't be recorded on demand, made by
// overriding single recorded responses (see testdata/scenarios/)
await captureFailedEpisode();
await captureInterruptedCrawl();

console.log(`done (${mode})`);
