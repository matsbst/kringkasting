# Codebase review — 2026-09-12

Reviewed revision: `eb2e261`. Scope: TypeScript/Preact/Fresh application, SQLite persistence, background crawling, HTTP caching, admin authentication, frontend interactions, and deployment configuration. The working tree was clean at the start.

## Executive summary

The previous review led to real improvements: the backlog now checks writes and pauses on transient manifest failures; search errors return 503; upstream concurrency is capped; accessibility fixes are present. However, public request admission remains incomplete, the new admin surface needs stronger protection, and incremental refresh/cache races can still produce incomplete feeds.

This report records 12 actionable findings: two high-priority security issues, nine medium-priority security/performance/correctness issues, and one low-priority admin correctness issue. Several security risks depend on deployment conditions, which are stated explicitly. No direct SQL injection, attacker-controlled script execution, or authentication bypass was demonstrated.

Validation: 43 tests passed; formatting, lint, type checks, and production build passed with local Deno 2.9.6. Controlled reproductions used an in-memory database, synthetic credentials, stubbed upstream responses, and direct requests through the production build. No production data was changed. Docker's pinned Deno 2.5.4, the live proxy/CDN configuration, browser/screen-reader behavior, and dependency advisory coverage were not validated.

## High priority

### R01 — Public entry points bypass request admission and can build an unbounded upstream queue

- **Rule/category:** resource exhaustion; security/performance. **Severity:** High / P1.
- **Locations:** [homepage search](routes/index.tsx#L26), [chapter endpoint](routes/api/feeds/[seriesId]/[episodeId]/chapters.ts#L36), [upstream queue](lib/http.ts#L22).
- **Evidence:** `/api/search` applies a token bucket, but `/?query=...` calls `nrkRadio.search(query)` directly. Chapter requests also always perform upstream lookups without a limiter, response cache, or request coalescing. `upstreamWaiters.push(resolve)` has no capacity limit; the 15-second timeout starts only after a slot is acquired. Client cancellation is not propagated to queued work.
- **Reproduction:** Exhausted one identity's API search allowance and received 429. Three subsequent unique homepage searches from that identity all returned 200 and made three upstream requests.
- **Impact:** An unauthenticated caller can queue arbitrarily many operations, consume memory, and delay unrelated searches/feed refreshes despite the 12-active-request cap. This also undermines the documented claim that listener traffic does not affect upstream load.
- **Fix:** Apply admission control consistently at each public entry point or a shared service boundary. Bound queue length and total wait time, reject overload, and cancel work when appropriate. Cache/coalesce chapter lookups and repeated concurrent searches.
- **Mitigation/limits:** Edge rate limits can reduce exposure, but they must cover the homepage query route and chapters as well as `/api/search`; live edge rules were not inspected. No production load test was performed.

### R02 — TLS-terminated deployments can issue an admin credential cookie without Secure

- **Rule/category:** session transport. **Severity:** High / P1 when deployed behind an HTTP upstream proxy.
- **Location:** [tryLogin](lib/admin-auth.ts#L51).
- **Evidence:** `Secure` is determined exclusively by `new URL(request.url).protocol`. The configured public HTTPS origin is not considered. The cookie contains the configured admin token itself and is valid for 30 days.
- **Reproduction:** In the production build, with `APP_ORIGIN=https://example.test`, a successful login arriving as `http://localhost/admin` returned a cookie without `Secure`.
- **Impact:** In that deployment, the browser can send the reusable admin credential over an HTTP request to the host. Credential exposure would grant refresh, recrawl, deletion, and cleanup access.
- **Fix:** Derive cookie security from an explicit trusted deployment setting/public origin, with an intentional local-development exception. Do not trust arbitrary forwarded-protocol headers. Prefer revocable session identifiers instead of distributing the master token as the session.
- **Mitigation/limits:** If the actual proxy guarantees HTTPS request URLs or rewrites cookie attributes, this particular deployment is protected. Verify the real login response; the live proxy was not tested. This is not a finding against intentionally HTTP-only local development.

## Medium priority — security and performance

### R03 — Rate-limit identities trust unverified request headers

- **Rule/category:** proxy trust boundary. **Severity:** Medium / P2.
- **Location:** [getClientKey](lib/rate-limit.ts#L59).
- **Evidence:** `cf-connecting-ip` takes precedence over the first `x-forwarded-for` value, without verifying the connecting peer or validating the address. Without either header, every caller shares `unknown`.
- **Reproduction:** Changing the supplied header changed the identity and obtained a fresh bucket after the previous bucket was exhausted.
- **Impact:** Directly exposed Docker instances, or proxies that preserve injected headers, allow trivial quota bypass. Header-free deployments instead unfairly share one quota among all users.
- **Fix:** Pass the actual peer address through trusted middleware; accept forwarded addresses only from configured trusted proxies and according to that proxy's overwrite/append policy. Normalize and validate addresses.
- **Mitigation/limits:** Cloudflare-origin restrictions and verified header rewriting can mitigate this. It is a confirmed application-level trust gap, not proof the deployed Cloudflare path is bypassable.

### R04 — Admin login has no throttling or request-size guard

- **Rule/category:** authentication abuse. **Severity:** Medium / P2.
- **Locations:** [login handler](routes/admin.tsx#L66), [token configuration](lib/admin-auth.ts#L13).
- **Evidence:** Every POST parses the form and hashes a submitted token without applying the existing limiter. The token configuration accepts strings as short as eight characters.
- **Reproduction:** Forty incorrect login submissions from the same identity reached the production handler without any 429 response.
- **Impact:** Online guessing is unrestricted at the application layer; its practicality depends on token entropy. Form parsing and response rendering also provide an unnecessary abuse surface.
- **Fix:** Add bounded body/token lengths and login throttling with trusted client identity plus a carefully chosen global abuse budget. Require/document a generated high-entropy secret. Avoid a global lockout that attackers can use to deny the administrator access.
- **Mitigation/limits:** Admin is correctly disabled when no token is configured. A long random token makes guessing impractical, and edge controls may help. [OWASP authentication guidance](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html) recommends login throttling.

### R05 — Authenticated admin HTML misses the intended no-store policy

- **Rule/category:** sensitive response caching. **Severity:** Medium / P2.
- **Location:** [admin GET](routes/admin.tsx#L59).
- **Evidence:** `noStore()` is used only for redirects, while dashboard and login HTML return `page(...)` directly.
- **Reproduction:** An authenticated GET through the production build returned 200 with no `Cache-Control` or `Vary` header.
- **Impact:** Operational information is allowed to persist in browser caches, and a deployment that caches HTML could serve a cookie-authenticated representation outside the intended session. No actual shared-cache disclosure was demonstrated.
- **Fix:** Set `Cache-Control: no-store` on every `/admin` response, including rendered pages, failed login responses, redirects, and errors. Verify with a production response test.
- **Mitigation/limits:** Keep `/admin` excluded from edge caching. `noindex` controls indexing, not storage. The current documented feed-only CDN rule does not itself cache `/admin`.

### R06 — MAX_BUCKETS is not a limit, and cleanup becomes a hot-path full scan

- **Rule/category:** bounded memory and CPU. **Severity:** Medium / P2.
- **Location:** [bucket cleanup](lib/rate-limit.ts#L17).
- **Evidence:** Above 5,000 entries, every request scans the whole map. Only entries idle for ten minutes are removed; new entries are inserted even when all existing entries are fresh.
- **Impact:** A burst of distinct clients/identities grows the map without a hard bound. Sustained creation of fresh identities makes cumulative cleanup work quadratic. This compounds R03 but also occurs with legitimate high-cardinality traffic.
- **Fix:** Enforce an actual capacity policy and amortize expiry cleanup instead of scanning on every call. Choose an eviction/overflow policy that does not simply give attackers unlimited fresh buckets. Apply hard bounds to the negative-result cache as well; its size threshold also only triggers expiry pruning.
- **Mitigation/limits:** Static code finding; no production memory exhaustion was attempted. Low traffic will not normally trigger this path.

### R07 — Warm 304 feed polls still synchronously materialize every episode

- **Rule/category:** avoid repeated hot-path work. **Severity:** Medium / P2.
- **Locations:** [fetchSeries](lib/caching.ts#L137), [readSeries](lib/storage.ts#L159), [renderFeed](lib/feed-cache.ts#L26).
- **Evidence:** The route obtains the full `Series` before consulting the rendered-feed cache. `readSeries` executes a synchronous query for every episode and creates a Date/object per row, even when an unchanged ETag will produce 304.
- **Reproduction:** Thirty warm conditional requests for a synthetic 10,000-episode archive produced thirty full series reads, all returned 304, and took approximately 310 ms locally. This is an in-memory microbenchmark, not a production capacity estimate.
- **Impact:** Polling cost scales with archive length, and synchronous SQLite work blocks other requests. The XML cache removes serialization/hashing but not most database hydration work.
- **Fix:** Read lightweight freshness/version metadata first. Serve a valid rendered-cache entry without loading episodes; hydrate the archive only for refresh or rendering. Give the feed cache a byte budget as well as an entry count.
- **Mitigation/limits:** CDN caching reduces origin traffic. Measure again with representative archives and the deployment runtime before setting performance targets.

## Medium priority — correctness and data integrity

### R08 — Partially successful refreshes permanently hide later pages

- **Location:** [getNewEpisodes](lib/nrk/nrk.ts#L307). **Severity:** Medium / P2.
- **Evidence:** A failure after page one returns collected episodes as success. After these are persisted, the next refresh sees them on page one and stops at `sawKnownEpisode`. Blocked IDs are also mixed into the stop set, and hitting the ten-page cap has no persisted continuation.
- **Reproduction:** Page one returned `new`; page two returned 503. After storing `new` and recovering page two, the next refresh returned no episodes and never requested page two again.
- **Impact:** Completed archives can acquire permanent gaps following an ordinary upstream hiccup or a sufficiently large publication burst.
- **Fix:** Preserve a refresh cursor/completion state and distinguish already-stored episodes from blocked IDs. Do not report a partial batch as a completed refresh. Cover both cheap and weekly full-metadata refresh paths; the latter still reads only the initial listing for ordinary series.

### R09 — Catalog fallback still converts some upstream outages into cached 404s

- **Location:** [getSeriesData](lib/nrk/nrk.ts#L269). **Severity:** Medium / P2.
- **Evidence:** Only the final catalog family's statuses survive the loop. A failure in the correct family followed by 404 in the alternative family becomes `null`. HTTP 429 is also not classified as transient.
- **Reproduction:** Podcast endpoints returned 503 and series endpoints returned 404. `caching.getSeries` returned null, and the second request made zero upstream requests because of negative caching.
- **Impact:** Existing podcasts appear nonexistent for ten minutes during partial outages/rate limiting. The new all-503 test does not cover this mixed outcome.
- **Fix:** Aggregate outcomes across both families, classify 429 as retryable, and negative-cache only a positively established absence. Add mixed-family and rate-limit tests.

### R10 — Feed rendering can cache an old snapshot under the latest data version

- **Locations:** [renderFeed](lib/feed-cache.ts#L24), [crawl scheduling](lib/caching.ts#L152). **Severity:** Medium / P2.
- **Evidence:** A `Series` snapshot and its data version are read separately. The crawler starts before `getSeries` resolves and may write episodes between those reads.
- **Reproduction:** With an immediately resolved crawl page, the actual caching function returned zero episodes while storage already contained one. Rendering that returned snapshot cached empty XML under version 2; a subsequent call with the current stored series still returned the empty cached XML.
- **Impact:** Newly crawled episodes are hidden until another version change or cache eviction. On a finished archive with no new writes, this can survive multiple ordinary refreshes.
- **Fix:** Associate the version atomically with the snapshot. Reject a stale snapshot at render time or reload it; do not label detached data with the database's later version. Add a deterministic crawl/render interleaving test.

### R11 — Deleting an actively crawling series leaves orphan episodes

- **Locations:** [deleteSeriesById](lib/storage.ts#L432), [crawl page persistence](lib/backlog.ts#L98). **Severity:** Medium / P2.
- **Evidence:** Deletion does not cancel or invalidate in-flight crawl work. Episode inserts have no enforced parent foreign key and can occur after the series row has been deleted.
- **Reproduction:** Held a crawl page pending, deleted the series, then completed the page. Storage ended with no series row and one orphan episode, while the crawler logged completion.
- **Impact:** Deletion does not remove all associated data reliably. Orphans are not discovered by cleanup, which enumerates series rows; pending initial/refresh requests can also race deletion.
- **Fix:** Use per-series generation/cancellation checks before committing asynchronous results. Enforce referential integrity and make page/parent-state checks transactional. Test deletion and recrawl while work is pending.

## Low priority

### R12 — Admin “refresh” can report success without making a request

- **Locations:** [expireSeries](lib/storage.ts#L453), [adaptive freshness](lib/caching.ts#L143). **Severity:** Low / P3.
- **Evidence:** Expiration subtracts 25 hours, but the dormant interval plus jitter can be 27.6 hours. The admin notice always says updated, including when upstream failure causes stale data to be served.
- **Reproduction:** With high jitter, expiring a completed dormant series and requesting it performed zero upstream requests.
- **Fix:** Add an explicit force-refresh option and return a refresh outcome. Avoid manipulating the timestamp that also drives garbage collection.

## Additional hardening and verification work

- **Admin origin validation:** `SameSite=Strict` is not a same-origin guarantee. A supplied sibling-origin POST with a valid synthetic cookie was accepted in the production handler. Browser exploitation requires control of a same-site origin and an authenticated administrator; neither condition was established for the live deployment. Validate trusted Origin/CSRF tokens for mutations. [OWASP CSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html).
- **Admin script isolation:** The global Umami script also loads on the admin login and dashboard. Exclude it there to reduce the number of systems trusted with password-entry DOM and authenticated actions. No analytics compromise or credential collection was observed. CSP is not present in the app; verify edge policy and consider a tested report-only rollout.
- **Cookie parsing:** `decodeURIComponent` throws on malformed cookie values. `kringkasting_admin=%` produced a 500. Treat malformed cookies as unauthenticated. Replace the “constant-time enough” hash-string comparison with a suitable constant-time comparison; no timing exploit was demonstrated.
- **Test isolation:** The nominally offline suite logged a real `fetch failed` after its fetch stub was restored while a background crawl was still active. Await/drain crawl work before restoring stubs; add a deny-by-default network fixture. Current tests omit admin response policies, proxy identity handling, full refresh recovery, and crawl/render/delete races.
- **Retries and failure records:** Expiring a backoff entry does not schedule a retry. Completed archives will generally not revisit failed episodes outside the refresh window; successful retries also do not clear the failure row. Define whether retries are promised and implement an explicit due-work queue if so.
- **Deployment reproducibility:** CI uses moving `v2.x`; Docker runs pinned 2.5.4; this review ran 2.9.6. Test the actual runtime image and maintain a deliberate patch update policy. No complete dependency/CVE audit was performed.
- **Privacy copy:** The page says app choice is never sent to the server, while subscribe controls emit Umami events including app name. Align wording with actual configured collection. This is a factual consistency observation, not a legal conclusion.

## Suggested repair order

1. Close request-admission bypasses; bound waiting work and identity storage.
2. Protect admin cookies, login, response caching, and mutation origins.
3. Fix refresh progress and versioned snapshots; coordinate deletion with active work.
4. Make warm feed polling independent of archive length, then measure on the production runtime.
5. Add deterministic regression coverage for these boundaries and isolate background tasks in tests.

Temporary local reproduction scripts: `/private/tmp/nrss-review-20260912.ts`, `/private/tmp/nrss-admin-review-20260912.ts`, and `/private/tmp/nrss-races-review-20260912.ts`. They use synthetic data only. Application source was not modified by this review.
