# Test data for the Go migration

See `docs/go-migration.md`, step 1. Everything here was captured from the
current Deno app and is what the Go rewrite is compared against.

## Contents

- `nrk/` — recorded NRK API responses (one JSON file per request: method,
  URL, status, a few headers, body). Recorded 2026-10-15.
- `baseline/http/` — golden HTTP responses from the Deno app, replayed
  offline against `nrk/`. Each file holds the request, the status line,
  sorted response headers and the body. Clock-dependent headers (`date`,
  `expires`, `last-modified`) and per-request script nonces are replaced
  by `<dynamic>`.
- `scenarios/` — overrides for single recorded NRK responses, for failures
  that can't be recorded on demand: `failed-episode` (one playback
  manifest returns 404) and `interrupted-crawl` (archive page 2 returns
  503, then a restart two days later with NRK reachable). Their outcomes
  are in `baseline/http/scenario_*`, including `*_state.txt` dumps of the
  durable database state (series, episodes, failed episodes, app_state
  keys) without wall-clock timestamps.
- `baseline/deno-created.sqlite3` — the database the Deno app wrote while
  producing the goldens. Used to test that Go opens a Deno-created
  database.

## Test cases

| Feed                                                   | Covers                                                         |
| ------------------------------------------------------ | -------------------------------------------------------------- |
| `supertroett`                                          | ordinary podcast, 6 episodes                                   |
| `doemt`                                                | sequential podcast, 7 episodes                                 |
| `musikkhistorier`                                      | umbrella podcast with customSeasons, 12 episodes               |
| `musikkhistorier/sesong/miles-davis--bak-mytene`       | season feed                                                    |
| `musikkhistorier/sesong/nick-cave--moerkets-lysmester` | season feed                                                    |
| `radioteatret-nrk-p1pluss`                             | radio series, HLS only (stream-only), 55 episodes over 2 pages |

Plus conditional GETs, HEAD/POST/trailing slash/invalid and unknown IDs,
chapters, search, pages, static file headers, admin flows (enabled and
disabled) and origin handling with and without `APP_ORIGIN`.

## Bugs fixed in the Deno app before capture

The first capture exposed three bugs, fixed in the Deno app so that the
goldens show correct behaviour for Go to match:

- Feeds link chapters by NRK's hash `id`, but NRK's episode endpoint only
  answers to `episodeId`, so chapters never resolved. The chapters
  endpoint now maps the hash id to `episodeId` via the stored share link;
  feed XML is unchanged (`chapters_*_feed_link`).
- An unauthenticated admin POST returned the login page without
  `Cache-Control: no-store` (`admin_action_unauthenticated`).
- Error pages rendered by Fresh (404s, disabled `/admin`, wrong methods)
  lacked the security headers (`page_not_found`, `admin_disabled`).

## Regenerating

The tools in `tools/baseline/` run the built Fresh app with a fetch
interceptor, a pinned clock and a seeded random generator:

```sh
deno task build
deno run -A tools/baseline/capture.ts replay   # offline, must produce no diff
deno run -A tools/baseline/capture.ts fill     # records only missing NRK responses
deno run -A tools/baseline/capture.ts record   # re-records everything from NRK
```

Replay is deterministic: two runs must give identical `baseline/http/`.
