/**
 * Runs the built Fresh app (_fresh/server.js) behind the baseline harness.
 *
 *   NRK_FIXTURES=record|replay FIXTURE_DIR=testdata/nrk PORT=8765 \
 *   KRINGKASTING_DB_PATH=/tmp/x.sqlite3 deno run -A tools/baseline/serve.ts
 */
import { FIXED_START_MS, type FixtureMode, installFixtures, pinClock, seedRandom } from "./harness.ts";

const mode = (Deno.env.get("NRK_FIXTURES") ?? "replay") as FixtureMode;
const dir = Deno.env.get("FIXTURE_DIR") ?? "testdata/nrk";
const overlay = Deno.env.get("FIXTURE_OVERLAY") || undefined;
const port = Number(Deno.env.get("PORT") ?? "8765");
// a later start simulates a restart after time has passed (retry deadlines)
const startOffsetMs = Number(Deno.env.get("CLOCK_OFFSET_MS") ?? "0");

pinClock(FIXED_START_MS + startOffsetMs);
seedRandom();
installFixtures(mode, dir, overlay);

// import after patching so module-level state sees the pinned clock
// (a variable path keeps `deno check` out of the generated bundle)
const entry = "../../_fresh/server.js";
const { default: server } = await import(entry);

// reseed per request so random picks (homepage suggestions) repeat exactly
const handler = (request: Request, info: Deno.ServeHandlerInfo) => {
  seedRandom();
  return server.fetch(request, info);
};

Deno.serve(
  { hostname: "127.0.0.1", port, onListen: () => console.log(`baseline server (${mode}) on :${port}`) },
  handler,
);
