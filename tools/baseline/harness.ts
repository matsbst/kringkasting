/**
 * Test harness for capturing the Deno app's behaviour before the Go
 * rewrite (docs/go-migration.md, step 1). Not part of the app.
 *
 * - Intercepts every outbound fetch. In "record" mode it passes requests
 *   through to NRK and saves each response as a fixture; in "replay" mode
 *   it answers only from those fixtures, so runs are offline and
 *   repeatable.
 * - Pins the clock to a fixed start time (advancing in real time from
 *   there) and seeds Math.random, so refresh decisions and the homepage's
 *   random suggestions don't drift between runs.
 */

/** record: always hit NRK; replay: fixtures only; fill: replay, recording only what is missing */
export type FixtureMode = "record" | "replay" | "fill";

export type Fixture = {
  method: string;
  url: string;
  status: number;
  headers: Record<string, string>;
  body: string;
};

/** response headers worth keeping; everything else is CDN noise */
const KEPT_HEADERS = ["content-type", "content-length", "retry-after", "location"];

/** 2026-10-15T12:00:00Z, the day the fixtures were recorded */
export const FIXED_START_MS = Date.UTC(2026, 9, 15, 12, 0, 0);

export function fixtureKey(method: string, url: string): string {
  const parsed = new URL(url);
  const readable = `${parsed.host}${parsed.pathname}${parsed.search}`
    .replace(/[^a-zA-Z0-9._-]+/g, "_")
    .slice(0, 120);
  return `${method.toLowerCase()}__${readable}__${shortHash(`${method} ${url}`)}`;
}

function shortHash(text: string): string {
  // FNV-1a, 32 bit: stable and dependency-free; collisions are caught
  // by the url stored inside each fixture
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash.toString(16).padStart(8, "0");
}

/**
 * `overlayDir`, when given, is checked before `dir`: scenario fixtures
 * (an NRK 404 or 503 in place of a recorded success) live there.
 */
export function installFixtures(mode: FixtureMode, dir: string, overlayDir?: string) {
  const realFetch = globalThis.fetch.bind(globalThis);

  globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const method = request.method.toUpperCase();
    const url = request.url;
    const name = `${fixtureKey(method, url)}.json`;
    const path = `${dir}/${name}`;

    if (mode !== "record") {
      let fixture: Fixture | null = null;
      for (const candidate of overlayDir ? [`${overlayDir}/${name}`, path] : [path]) {
        try {
          fixture = JSON.parse(await Deno.readTextFile(candidate));
          break;
        } catch { /* try the next one */ }
      }
      if (!fixture && mode === "fill") {
        return await recordFixture(realFetch, request, method, url, path);
      }
      if (!fixture) {
        console.error(`[fixtures] MISSING ${method} ${url}`);
        return new Response("fixture missing", { status: 599, headers: { "x-fixture-missing": "1" } });
      }
      if (fixture.url !== url) {
        throw new Error(`[fixtures] hash collision: ${url} vs ${fixture.url}`);
      }
      const body = method === "HEAD" || fixture.status === 204 || fixture.status === 304 ? null : fixture.body;
      return new Response(body, { status: fixture.status, headers: fixture.headers });
    }

    return await recordFixture(realFetch, request, method, url, path);
  };
}

async function recordFixture(
  realFetch: typeof fetch,
  request: Request,
  method: string,
  url: string,
  path: string,
): Promise<Response> {
  const response = await realFetch(request);
  const body = method === "HEAD" ? "" : await response.clone().text();
  const headers: Record<string, string> = {};
  for (const name of KEPT_HEADERS) {
    const value = response.headers.get(name);
    if (value !== null) headers[name] = value;
  }
  const fixture: Fixture = { method, url, status: response.status, headers, body };
  await Deno.writeTextFile(path, JSON.stringify(fixture, null, 2) + "\n");
  console.log(`[fixtures] recorded ${method} ${url}`);
  return response;
}

export function pinClock(startMs = FIXED_START_MS) {
  const RealDate = Date;
  const offset = startMs - RealDate.now();
  const now = () => RealDate.now() + offset;

  class PinnedDate extends RealDate {
    constructor(...args: unknown[]) {
      // @ts-ignore: forward any Date constructor signature
      super(...(args.length === 0 ? [now()] : args));
    }
    static override now() {
      return now();
    }
  }
  globalThis.Date = PinnedDate as DateConstructor;
}

export function seedRandom(seed = 42) {
  // mulberry32
  let state = seed >>> 0;
  Math.random = () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
