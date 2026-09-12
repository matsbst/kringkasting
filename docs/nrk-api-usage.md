# Use of NRK's API (psapi.nrk.no)

Kringkasting generates public RSS feeds from NRK metadata. Audio and artwork are served directly by NRK's CDNs; this application stores metadata, episode links, chapters, and crawl progress, not media files.

## Request-driven refresh policy

There is no timer polling every saved show. A feed request reaching the application checks whether a refresh is due. Browser/CDN caching can delay that request further. Unrequested archives remain idle and are retained indefinitely, avoiding a full recrawl when someone returns. Individual series can still be deleted manually in the admin dashboard.

| Age of newest episode | Base refresh interval |
| --------------------- | --------------------- |
| Under 2 days          | 3 hours               |
| 2–30 days             | 12 hours              |
| 30–180 days           | 3 days                |
| 180 days or more      | 7 days                |
| No saved episodes     | 3 hours               |

The interval has ±15% jitter. New episodes in a long-dormant show may therefore take several days to appear. Metadata such as title/artwork is refreshed after a week when a refresh is requested. Umbrella shows still use the full metadata/season path on each due refresh.

## Requests and caches

| Operation            | Upstream work and cache policy                                                                                                              |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| Search               | Results cached in memory for 5 minutes per query                                                                                            |
| Discovery catalog    | Complete catalog persisted in SQLite for 7 days, loaded on demand; restarts reuse it                                                        |
| Episode refresh      | Usually one listing request, following additional pages only until the original saved-episode boundary is reached                           |
| New playable episode | One playback manifest lookup and one HEAD request for enclosure byte size                                                                   |
| Chapters             | Successful results, including no chapter points and confirmed missing episodes, cached in SQLite for 7 days; simultaneous lookups coalesced |
| Feed polling         | Rendered XML/ETag cached locally and advertised to shared caches for an hour                                                                |

Chapter responses allow one day of browser/CDN caching. On an upstream outage, a saved chapter response remains available; an uncached outage returns 503 and is not cached as missing. A partial catalog refresh never replaces the previous complete catalog.

## Archive and refresh recovery

The initial archive crawl runs one series at a time, with a one-second pause between pages and at most 200 pages per run. Known episode IDs skip repeated manifest and file-size lookups. Progress is saved in SQLite. Resuming revisits the last completed page to provide overlap if pagination shifted.

Incremental refreshes have their own durable checkpoint and original episode boundary. If a page/manifest fails or the ten-page batch limit is reached, the next eligible feed request resumes that checkpoint with one page of overlap. It does not reopen a full archive scan. The boundary is recorded before new episodes are persisted, so a restart cannot mistake a partially saved batch for a complete refresh.

## Failure backoff

Refresh jobs, archive jobs, catalog loads, chapter loads, and failed upstream URLs have persistent retry deadlines. Repeated failures wait 5 minutes, 10 minutes, 20 minutes, and so on, capped at one day. Successful work clears its retry state. Retry eligibility does not start a background timer: another relevant request is needed after the deadline.

HTTP 429/5xx responses honor `Retry-After` as either seconds or an HTTP date. An explicit future deadline also pauses new requests to that upstream origin; requests already in flight may finish. Restarts preserve these deadlines. Queued requests recheck them before starting.

Definitively unavailable playback manifests retain the separate existing episode backoff of 1, 2, 4, … up to 30 days. Those episode IDs become eligible when encountered again; this is not a separate scheduled retry crawler.

The global limit remains 12 active upstream requests and 64 queued requests. Interactive episode work uses up to 6 workers and backlog work up to 3. Fetch timeouts are 15 seconds after acquiring a slot.

## Expected reduction and measurement

Changing a continuously requested dormant show's base interval from daily to weekly reduces its routine checks by about 86%. Moving active shows from hourly to every three hours reduces those checks by about 67%. These figures exclude new-episode manifests, HEAD requests, metadata refreshes, failures, searches, and chapters.

Chapter reuse, persistent catalog caching, retained archives, and checkpoint recovery remove redundant requests without changing the episode archive's intended completeness. Actual totals depend on usage and archive sizes; use the admin dashboard's upstream category counters to measure them. Counters reset on application restart, while caches and retry deadlines persist.
