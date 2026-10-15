# Migration to Go

Date: 2026-10-15
Status: done. Released to kringkast.ing on 2026-10-15 (`fe5c839`); the Deno code was removed afterwards. Kept as the record of how the migration was done and verified.

## Why Go

Deno development ends around October 2027, so the runtime has to change. The priorities for the replacement are **low maintenance over many years** and **low resource use**, so that the project is easy to self-host. Preserving the existing TypeScript code is not a goal.

- The standard library covers the HTTP server, templates, XML, JSON, concurrency and testing. The only expected third-party runtime dependency is the SQLite driver. Go 1's compatibility promise keeps upkeep low.
- One static binary with templates and assets embedded. Expected (a hypothesis until measured with representative archives): an image of tens of MB instead of `cloudron/base` + Deno, and memory use dropping from roughly 50–100 MB to roughly 10–25 MB.
- The background crawler (queue, concurrency caps, retry deadlines, checkpoints) maps directly onto goroutines, a semaphore and a process-owned `context`.

## Approach: one big bang

Kringkasting has few users, so the whole app is rewritten and released at once, with no staged releases and no Deno/Go coexistence. Most problems get fixed when they show up. Five kinds of failure are too costly to fix afterwards, so automated checks guard them before release:

1. **Feed identity.** Changed GUIDs, feed URLs or enclosure links make podcast apps re-download episodes, show duplicates or lose subscriptions, and a later fix doesn't undo that.
2. **Archive data.** Some archived episodes may no longer be available from NRK, so a damaged or wiped database can't be rebuilt by recrawling.
3. **NRK load.** A crawler bug that ignores the limits could get the server blocked by NRK.
4. **Security and HTTP behaviour.** Admin authentication (cookie flags `HttpOnly`, `Secure` behind HTTPS, `SameSite=Strict`, `Path=/admin`), `no-store` on every admin response, the 404 when admin is disabled, security headers, and conditional GETs (`If-None-Match` taking precedence over `If-Modified-Since`, 304 responses) and cache headers. A regression here is a security hole or a bandwidth problem, not a cosmetic one.
5. **Existing installations.** Pulling the new image onto an existing Compose or Cloudron install must work without the self-hoster changing anything (see Packaging).

Everything else (UI details, error page wording, admin layout, small search differences) is checked on a test instance before release and fixed afterwards if something slips through.

## Must not change

- Public URLs: feeds, season feeds, chapters, the home page, `/personvern`, `/admin`, `/api/search`, `/sitemap.xml` and static assets.
- Feed output: XML matches the current app byte for byte for the same data and origin, especially GUIDs, enclosures and links.
- The database: opened in place with the same schema and path (`/app/data/kringkasting.sqlite3`). No export/import and no recrawl. The old Deno image must still be able to open it after Go has written to it.
- NRK behaviour: concurrency caps, refresh intervals, retry deadlines and archive retention keep today's values.
- Configuration: `APP_ORIGIN`, `CLOUDRON_APP_ORIGIN`, `ADMIN_TOKEN`, `SENTRY_DSN`, `UMAMI_SRC`, `UMAMI_WEBSITE_ID`, `KRINGKASTING_DB_PATH`, and port 8000.
- Distribution: one Docker image for both Compose and Cloudron, with no new required services.
- The site keeps working without JavaScript.

Database redesign, UI redesign and new features are out of scope.

## Target stack

| Concern         | Choice                                                                                            | Notes                                                                                                                                  |
| --------------- | ------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| Toolchain       | Current stable Go, pinned with the `toolchain` directive in `go.mod` and the builder image digest |                                                                                                                                        |
| HTTP            | `net/http` with method-aware `ServeMux` patterns                                                  | No web framework.                                                                                                                      |
| HTML            | `html/template`, embedded with `embed.FS`                                                         | One layout replaces `_app.tsx`.                                                                                                        |
| Feed XML        | Custom XML writer ported from `lib/rss.ts` / `lib/xml.ts`                                         | Not `encoding/xml` marshalling, so the output can match byte for byte.                                                                 |
| SQLite          | `modernc.org/sqlite` (pure Go, no cgo)                                                            | Static binary. `mattn/go-sqlite3` is the fallback if it causes problems.                                                               |
| NRK API         | Structs written directly in the source, covering only the fields used                             | Replaces the tool-generated OpenAPI types and `generate-types.ts`. Checked against saved real NRK responses and the opt-in live tests. |
| Error reporting | Port of the existing minimal Sentry-protocol reporter (`lib/errors.ts`)                           | No SDK. Keep the scrubbing rules.                                                                                                      |
| Interactive UI  | Plain JavaScript enhancing server-rendered HTML; no JS build step                                 | Replaces the Preact islands.                                                                                                           |
| CSS             | Tailwind 4 standalone CLI in the Docker build stage, pinned                                       | No Node.                                                                                                                               |
| Tests           | `go test`, with golden files captured from the current app                                        | Live NRK tests behind a `live` build tag.                                                                                              |
| Lint/format     | `gofmt`, `go vet`, `staticcheck`                                                                  |                                                                                                                                        |

Keep dependencies minimal: the SQLite driver plus at most a few small, well-maintained modules.

### Interactive UI

The server renders the complete page; JavaScript only enhances it.

- **Search:** the form submits normally without JS. With JS, it fetches results from a **separate fragment endpoint** (e.g. `/api/search/fragment?q=`) that returns server-rendered HTML from the same Go template, so the result markup exists only once. `/api/search` keeps returning the same JSON as today; there is no content negotiation, so caches can't serve HTML to JSON clients. The script debounces input, cancels in-flight requests with `AbortController` and ignores responses for outdated queries.
- The JSON endpoint, fragment endpoint and server-rendered search use the same search service, cache and rate-limit buckets. Their combined traffic stays within today's limits. Tests exercise all three entry points together and verify upstream request counts and failure handling.
- **Subscribe:** the preferred app stays in `localStorage` under the same key, so existing users keep their choice. Use a native `<dialog>` and keep cross-tab synchronisation through the `storage` event.
- **Share:** use the Web Share API, falling back to the Clipboard API and then to a visible link.

## Architecture

```
cmd/kringkasting/     main: config, logging, signal handling, `healthcheck` subcommand
internal/config/      env parsing (APP_ORIGIN, CLOUDRON_APP_ORIGIN fallback, ...)
internal/store/       SQLite: open, WAL, additive migrations, queries (storage.ts)
internal/nrk/         NRK client, structs, rate limiting, retry (nrk.ts, http.ts, rate-limit.ts, retry.ts)
internal/crawl/       backlog queue, checkpoints (backlog.ts)
internal/catalog/     NRK catalog for the suggestion chips (catalog.ts)
internal/feed/        RSS, render cache, ETag/Last-Modified, refresh policy, chapters (rss.ts, feed-*.ts, caching.ts, chapters.ts)
internal/feedid/      feed id helpers (feed-id.ts)
internal/retry/       durable retry deadlines (retry.ts)
internal/stats/       admin counters, NRK health alert (stats.ts)
internal/web/         routes, middleware, pages, search, admin, rate limiting, static files
internal/report/      Sentry-protocol reporter
internal/app/         wiring, shared by main and the end-to-end tests
internal/e2e/         golden comparisons, concurrency tests, the opt-in Deno round trip
web/templates/, web/static/   embedded at build time (web/styles.css → static/assets via tools/build-css.sh)
```

One process and one in-memory crawl queue. Recover and report panics in handlers and background goroutines; if the process is left in an unsafe state, report it and exit so the container restarts.

### SQLite connections and transactions

Go's `*sql.DB` is a connection pool, not a single connection. The current code runs `BEGIN`/`COMMIT`/`ROLLBACK` as separate statements on one connection (`lib/storage.ts`). Ported naively through a pool, those statements could land on different connections. Rules:

- Every multi-statement write uses `db.BeginTx` and a `*sql.Tx`. Never run `BEGIN`/`COMMIT` with `Exec`.
- Writes go through a single connection: either `SetMaxOpenConns(1)` on the pool, or a dedicated one-connection write pool next to a small read pool. Pick one with a benchmark during the rewrite.
- Per-connection PRAGMAs (`busy_timeout`, `foreign_keys` if used) are set on every new connection through the driver's connection hook or DSN, not once at startup. `journal_mode = WAL` is persistent in the file but is still asserted at open.
- Shared in-memory state (feed cache, crawl queue, rate-limit buckets, stats) is protected by a mutex or owned by a single goroutine.
- Concurrent requests for the same stale feed share one refresh operation. Preserve existing request coalescing for chapter and catalog fetches too. Tests must assert that simultaneous requests do not multiply upstream NRK calls, including when the shared operation fails.
- Tests run with `go test -race`, including a test that serves concurrent feed requests while a crawl writes.

### Background work and shutdown

- Crawls run on a context owned by the process, created in `main`, never one derived from the HTTP request. A handler only enqueues work, so the crawl continues after the response completes and after a client disconnects.
- Every NRK request has its own timeout, as today.
- On SIGTERM: stop accepting requests, cancel the process context, give in-flight work a fixed deadline (e.g. 10 s) to finish its current page and write its checkpoint, then close the database. Work that doesn't finish resumes from the checkpoint on the next request, as today.
- Tests cover: crawl continues after the response; crawl continues when the client disconnects mid-request; shutdown leaves checkpoints consistent.

## Steps

### 1. Capture the current app's behaviour

1. Create a migration branch, leaving the unrelated design files untouched.
2. Save real NRK responses as fixtures: ordinary podcasts, umbrella/season feeds, HLS-only and mixed media, failed episodes and partial crawls.
3. Run the current Deno app against those fixtures and save its output as golden files: feed and season feed XML (with headers), conditional GET responses (304s), chapters JSON, search JSON, sitemap, security headers on pages and static files, and the admin flows (disabled 404, login, wrong token, logout, form actions with their 303s, cookies and `no-store`).
4. Take a WAL-safe copy of the production database. Never work against the live file.
5. Record the current image size and memory use, for comparison.

#### Baseline measured (2026-10-15)

Deno image built from the current Dockerfile, run locally with a copy of the production database (47 feeds, 33,452 episodes). The image is amd64-only (`cloudron/base`), so on Apple silicon it runs under emulation; memory figures are indicative only.

|                                                   | Deno            |
| ------------------------------------------------- | --------------- |
| Image download / unpacked                         | 879 MB / 3.5 GB |
| Memory idle                                       | 56 MiB          |
| Memory peak, 3 rounds of all 47 feeds in parallel | 410 MiB         |
| Memory 10 s after the load                        | 245 MiB         |
| All 47 feeds once, sequentially (41 MB of XML)    | 7 s             |

The peak exceeds Cloudron's 384 MiB `memoryLimit`. The same load script measures the Go image for comparison.

### 2. Rewrite in Go

Port everything in dependency order: config → store → NRK client → crawler → feeds and chapters → pages, search and UI → admin → error reporting. These automated checks must pass before release:

- **Feeds:** generated feed XML and headers are byte-identical to the golden files, including 304 responses to conditional GETs.
- **Security and HTTP:** admin flows, cookies, `no-store`, security headers (including on static files) and search JSON match the golden files.
- **Database round trip (release gate):** on a copy of the production database, run Deno → Go (with crawl and refresh writes) → Deno. After each step, dump every table (`series`, `episodes`, `failed_episodes`, `app_state` and any others) as rows ordered by primary key and compare field by field against the expected values: episode IDs, URLs, timestamps, season metadata, archive cursors and completion flags, retry deadlines, refresh checkpoints and cache entries. Rows Go was not meant to touch must be identical; rows it wrote must have the values the Deno code would have written. Also run `PRAGMA integrity_check`.
- **NRK limits:** tests assert that the concurrency caps, refresh intervals, retry deadlines and retention match today's values, and that a mocked multi-page crawl respects them.
- **Concurrency:** `go test -race` passes, including concurrent feed requests during a crawl.

Also port the offline tests that encode behaviour (`storage`, `caching`, `crawl-policy`, `feed-id`, `rss`, `security-fixes`, `utils`, `nrk.offline`) where they still apply.

Packaging:

- Multi-stage Dockerfile producing a static binary on a small Alpine base, with only `/app/data` writable. The same image must work under Compose and Cloudron.
- **Keep `curl` in the image** for this release. Existing user `compose.yaml` files override the image healthcheck with `curl -fsS http://localhost:8000/`, and removing `curl` would leave those installs permanently unhealthy. The image's own `HEALTHCHECK` can use a `kringkasting healthcheck` subcommand; dropping `curl` can be reconsidered later with advance notice.
- **Data ownership on existing volumes.** Today `start.sh` starts as root, `chown`s `/app/data` to the `cloudron` user and then drops privileges with `gosu`. The Go image must keep that behaviour, using the same user and UID so existing files stay readable: start as root, fix ownership, then drop to that user (a small entrypoint, or the binary itself before opening the database). Test by pulling the new image onto a volume created by the current image, for both Compose and Cloudron.
- CI runs `go vet`, `staticcheck`, `go test` and the image build. Switch Dependabot from Deno to Go modules. Update the README, `.env.example`, the Cloudron manifest and the docs.

### 3. Release

1. Run the new image against a copy of the production database on a test instance, and click through the site: search, subscribe, share, the no-JS paths, admin and a few feeds in a real podcast app.
2. Stop the old app cleanly and back up the database, including WAL state.
3. Deploy the Go image on the same volume.
4. Check that feeds, chapters, search and admin work and that NRK traffic looks normal. Test the origin directly as well as through the CDN.
5. Fix issues as they appear. For a serious problem, switch back to the pinned Deno image on the same database. Restore the backup only if the database itself is damaged, since restoring discards writes made after the switch.

### Results (2026-10-15)

All automated checks in step 2 pass:

- **Feeds:** byte-identical to the golden files for all six test feeds, their 304 responses and the failure scenarios; also for all 47 production feeds (33,452 episodes), compared with Deno serving the same database.
- **Database round trip:** Deno and Go ran the same flows on separate copies of the production database. Their written rows match field by field (wall-clock timestamps compared by presence), all 33,847 existing rows were untouched, and Deno serves every feed from the Go-written database identically. (The round-trip test was `internal/e2e/roundtrip_test.go`, removed with Deno.)
- **Pages:** screenshots of every page are pixel-identical to Deno (desktop and mobile); the homepage differs only in its random suggestion chips. 22 browser checks of the JavaScript pass (the removed `tools/baseline/browser-check.ts`).
- **Upgrade:** the Go image started on a volume written by the Deno image (including an unchecked WAL), runs as the same `cloudron` user, and the old `curl` healthcheck answers 200.
- `go test -race ./...`, `go vet` and `staticcheck` pass; live NRK tests pass (`-tags live`).

Measured with the same load script and production copy as the baseline (`tools/load.sh`):

|                                                                     | Deno            | Go            |
| ------------------------------------------------------------------- | --------------- | ------------- |
| Image download / unpacked                                           | 879 MB / 3.5 GB | 15 MB / 50 MB |
| Memory idle (amd64, emulated)                                       | 56 MiB          | 49 MiB        |
| Memory peak, 3 rounds of all 47 feeds in parallel (amd64, emulated) | 410 MiB         | 142 MiB       |
| Memory peak, natively on arm64                                      | –               | 133 MiB       |
| All 47 feeds once, sequentially                                     | 7 s             | under 1 s     |

The plan's 10–25 MB idle estimate was too low: the idle figure includes SQLite's page caches and the Go runtime, and the peak is mostly the in-memory cache of rendered feeds (41 MB of XML, as in Deno) plus garbage-collector headroom. Two fixes found while measuring cut the peak from 214 to 133 MiB: responses are written without copying the cached XML, and the XML is rendered in one pass into a pre-sized buffer. The image sets `GOMEMLIMIT=256MiB` as a soft ceiling under Cloudron's 384 MiB limit.

Deliberate differences from the Deno version:

- `HEAD` on a feed reports the real `Content-Length` (Deno reported 0).
- Fresh's own markup is gone (island comments, `data-frsh-key`, the boot script); the logo link no longer claims `aria-current`, the footer link to the current page keeps `aria-current="page"`.
- New `/api/search/fragment` endpoint returns the rendered results area for the client-side search; `/api/search` still returns the same JSON.
- A failed admin refresh shows a notice instead of a 500 page.
- Versioned assets (`?v=build`) are cached as immutable.
- The subscribe choice also syncs across tabs (the `storage` event).
- `APP_BUILD_ID` replaces `DENO_DEPLOYMENT_ID` (still read as a fallback). `KRINGKASTING_DB_PATH` is now honoured when set (the old `start.sh` always overrode it). Optional `LISTEN_ADDR` (default `0.0.0.0:8000`).
- The image has its own `HEALTHCHECK` (`kringkasting healthcheck`), so `compose.yaml` no longer defines one; `curl` stays for existing compose files. `sqlite3` is included for backups.
- Multi-arch images (amd64 and arm64).

After the release the Deno source, the capture tools (`tools/baseline/`), the browser check and the Deno → Go round-trip test were removed; they are in git history at `3ad24c6`. The golden files in `testdata/` stay and keep the Go version honest. `tools/load.sh` is the load test used for the measurements.

## Done when

- Nothing at build or run time depends on Deno, npm or Node.
- All automated checks in step 2 pass, including the Deno → Go → Deno database round trip.
- The new image upgrades an existing Compose volume and an existing Cloudron install without manual steps, and the existing `curl` healthcheck passes.
- The test-instance click-through passes: search with and without JS, subscribe, share, admin login and actions, and feeds in a real podcast app.
- After release: feeds are unchanged for subscribers, archives are intact and NRK traffic is within the existing limits.
- Image size and memory use are measured with a representative archive and recorded.

## First release after the switch: video episodes

Superseded: video is passed through directly with NRK's own playlist URLs, without a Kringkasting redirect address. See [nrk-video-pass-through.md](nrk-video-pass-through.md).

## References

- [Deno announcement and support windows](https://deno.com/blog/cloudflare)
- [Go 1 compatibility promise](https://go.dev/doc/go1compat)
- [Go toolchain selection](https://go.dev/doc/toolchain)
- [`net/http` routing patterns](https://pkg.go.dev/net/http#hdr-Patterns)
- [`html/template`](https://pkg.go.dev/html/template), [`embed`](https://pkg.go.dev/embed)
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)
- [Tailwind CSS standalone CLI](https://tailwindcss.com/blog/standalone-cli)
- [Cloudron packaging](https://docs.cloudron.io/packaging/)
