# Use of NRK's API (psapi.nrk.no)

## What the service does

Kringkasting ([kringkast.ing](https://kringkast.ing)) generates open RSS feeds for NRK's podcasts, so listeners can subscribe in the podcast app of their choice. The service is non-commercial, open source (AGPL-3.0), and unaffiliated with NRK. **All audio and artwork is served directly from NRK's CDNs** (`podkast.nrk.no`, `gfx.nrk.no`) to the listener's client — we never proxy or cache media files.

The central design principle: **the number of listeners does not affect the load on NRK.** All subscriber polling is answered from our own database and CDN edge; NRK only sees the service's own scheduled refreshes.

## Endpoints used

| Endpoint                                                | When                               | Frequency                                                         |
| ------------------------------------------------------- | ---------------------------------- | ----------------------------------------------------------------- |
| `GET /radio/search/search?q=`                           | A user searches on the site        | Cached 5 min per query                                            |
| `GET /radio/search/categories/podcast`                  | Catalog listing (suggestion chips) | ~7 paged requests per day, total                                  |
| `GET /radio/catalog/{podcast\|series}/{id}`             | Series metadata (title/artwork)    | On first lookup, then at most weekly per series                   |
| `GET /radio/catalog/{podcast\|series}/{id}/episodes`    | New episodes                       | Adaptive per series: see the table below                          |
| `GET …/episodes?page=N&pageSize=50`                     | One-time archive crawl per series  | Sequential with a 1 s pause between pages; resumes if interrupted |
| `GET /playback/manifest/{podcast\|program}/{episodeId}` | Episode download link              | Once per new episode                                              |
| `HEAD` on the MP3 at `podkast.nrk.no`                   | File size for the RSS enclosure    | Once per new episode                                              |
| `GET …/episodes/{episodeId}`                            | Chapter data                       | Only when a podcast app requests chapters                         |

## Refresh frequency per series (adaptive)

How often a series is checked for new episodes depends on how active it is:

| Newest episode                  | Checked       |
| ------------------------------- | ------------- |
| < 2 days old                    | hourly        |
| < 30 days                       | every 6 hours |
| older (dormant/finished series) | once per day  |

With random jitter, so refreshes don't cluster on the hour. A series is also only checked as long as someone actually subscribes to it: series without requests are garbage-collected after 90 days.

## Load-limiting measures

- **Subscriber polling never reaches NRK**: RSS feeds are cached in our own database and at the CDN edge (ETag/304 toward clients). A series costs NRK the same with 1 or 10,000 subscribers.
- **Incremental refreshes**: a refresh fetches only the episode listing (1 request); manifest lookups are made only for episodes we don't already have.
- **Backoff for unavailable episodes**: episodes without a playable manifest (geo-blocked/expired) are retried with exponential backoff (1 → 30 days), not on every refresh.
- **Concurrency caps**: at most 6 concurrent requests for interactive lookups, at most 3 during archive crawling, and a global cap of 12 concurrent requests toward NRK across the whole service.
- **Request coalescing**: concurrent requests for the same series trigger one NRK lookup, not several.
- **Negative caching**: lookups of unknown series IDs are remembered for 10 min, so typos and scanning don't cause repeated traffic.
- **Timeouts** of 15 s on all requests; failures are handled with deferral, not aggressive retries.

## Typical footprint

For a typical self-hosted instance following a few dozen series: **on the order of 50–200 requests per day in total** against `psapi.nrk.no`, most of them simple episode-listing lookups. A dormant series costs ~2 requests per day; an active daily show ~24–48.
