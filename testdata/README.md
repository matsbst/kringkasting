# Test data

Recorded on 2026-10-15 from the Deno version, before the Go rewrite
(docs/go-migration.md). The Go tests in `internal/e2e` replay these NRK
responses and compare the application's output with the Deno version's.
The files are frozen: the capture tool was removed together with the Deno
code (it is in git history at commit `3ad24c6`, `tools/baseline/`).

## Contents

- `nrk/` — recorded NRK API responses, one JSON file per request (method,
  URL, status, a few headers, body).
- `scenarios/` — overrides for single responses, for failures that can't
  be recorded on demand: `failed-episode` (one playback manifest returns
  404) and `interrupted-crawl` (archive page 2 returns 503, then a restart
  two days later with NRK reachable).
- `baseline/http/` — the Deno version's HTTP responses. Each file holds the
  request, the status line, sorted response headers and the body.
  Clock-dependent headers (`date`, `expires`, `last-modified`) and script
  nonces are `<dynamic>`. `scenario_*_state.txt` are dumps of the durable
  database state without wall-clock timestamps.
- `baseline/deno-created.sqlite3` — the database the Deno version wrote;
  the store tests open it to prove compatibility.

## Test feeds

| Feed | Covers |
| --- | --- |
| `supertroett` | ordinary podcast, 6 episodes |
| `doemt` | sequential podcast, 7 episodes |
| `musikkhistorier` | umbrella podcast with customSeasons, 12 episodes |
| `musikkhistorier/sesong/miles-davis--bak-mytene` | season feed |
| `musikkhistorier/sesong/nick-cave--moerkets-lysmester` | season feed |
| `radioteatret-nrk-p1pluss` | radio series, HLS only (stream-only), 55 episodes over 2 pages |

Plus conditional GETs, HEAD/POST/trailing-slash/invalid/unknown ids,
chapters, search, pages, static file headers, admin flows (enabled and
disabled) and origin handling with and without `APP_ORIGIN`.

## Changing behaviour on purpose

When a change is meant to alter output (a new feed tag, say), update the
affected golden files in the same commit and say why in the message. Feed
identity (GUIDs, enclosure URLs, feed URLs) must never change.
