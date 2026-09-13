# Kringkasting – RSS feeds for NRK's podcasts

**[kringkast.ing](https://kringkast.ing)** — a self-hostable web app that generates open, public RSS feeds for NRK's podcasts via their [API](https://psapi.nrk.no/documentation/), so you can listen to them in whatever podcast app you prefer.

Kringkasting started as a fork of [olaven/nrss](https://github.com/olaven/nrss). Notable differences from upstream:

- **Full episode archives**: a background crawler pages through each series' complete backlog, so feeds aren't limited to the latest ~20 episodes (fixes upstream [issue #8](https://github.com/olaven/NRSS/issues/8))
- Storage on plain **SQLite** (built-in `node:sqlite`) instead of Deno KV
- **Conditional GETs**: feeds answer `304 Not Modified` to polling podcast clients, and send CDN-friendly caching headers
- Feeds carry real enclosure byte sizes, `atom:link rel=self`, `language`, and use the registered `audio/mpeg` MIME type
- Migrated from Fresh 1.x + twind to **Fresh 2 + Tailwind CSS 4** (vite-based build)
- Redesigned UI with instant search and one-tap subscribe (Apple Podcasts, Overcast, Pocket Casts, Castro, AntennaPod — your choice is remembered)
- Removed the Vipps donation integration
- All remote `https://` imports replaced with local code or `npm:`/`jsr:` packages
- Dockerized, image built by GitHub Actions

## Why?

NRK, Norway's government-funded public broadcaster, locks its podcasts into its own app instead of building on open standards like RSS. Kringkasting opens them back up.

## NRK API usage

The service is built to be a considerate consumer of NRK's open API: cached subscriber polls avoid NRK, refresh cadence adapts to how actively a show publishes, and audio/artwork is always served directly from NRK's own CDNs. [docs/nrk-api-usage.md](./docs/nrk-api-usage.md) documents every endpoint used, the exact request cadence, and all load-limiting measures.

## Local development

1. [Install Deno](https://docs.deno.com/runtime/getting_started/installation/) (2.x)
2. `deno install --allow-scripts`
3. `deno task dev`
4. Open [localhost:5173](http://localhost:5173)

Other tasks: `deno task check` (fmt, lint, types), `deno task test` (offline regression tests; `deno task test:live` checks NRK's live API), `deno task build` + `deno task start` (production build and serve).

## Running with Docker

Build and run:

```sh
docker build -t kringkasting .
docker run -d -p 8000:8000 -v kringkasting-data:/app/data --name kringkasting kringkasting
```

The app listens on port 8000 and stores its SQLite database in `/app/data`.

Configuration:

| Env var                              | Purpose                                                                                                                                                                                              |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `KRINGKASTING_DB_PATH`               | Path of the SQLite database holding podcast metadata and the crawled episode archives (default `/app/data/kringkasting.sqlite3` in the container). Losing it means archives get re-crawled from NRK. |
| `APP_ORIGIN` / `CLOUDRON_APP_ORIGIN` | Public origin used for absolute URLs inside the RSS feeds. Falls back to the request origin.                                                                                                         |
| `ADMIN_TOKEN`                        | Enables the admin dashboard at `/admin` (operational stats, NRK-traffic counters, archive health, per-series actions). Unset = admin disabled. Use a long random string.                             |
| `SENTRY_DSN`                         | Optional. Sentry-protocol DSN (works with self-hosted Bugsink) for server-side error reporting. Reports exceptions with the release SHA, scrubbed of IP/query/body; unset = disabled.                |

If the instance sits behind a CDN such as Cloudflare, see [docs/cloudflare.md](./docs/cloudflare.md) for the recommended edge-caching setup.

## Crawling and retention

Feed refreshes are request-driven: every 3 hours for recently active shows, 12 hours after two days without an episode, 3 days after a month, and weekly after six months (with ±15% jitter). Titles/artwork refresh weekly; umbrella shows also fetch season metadata on each due refresh.

The discovery catalog and chapter results are cached in SQLite for a week. Interrupted refreshes resume a saved checkpoint, and failures use persistent backoff starting at five minutes while respecting upstream `Retry-After`. Saved archives are retained indefinitely unless manually deleted, so database storage grows with the shows followed. Keep the database volume across upgrades to preserve these savings.

See [NRK API usage](docs/nrk-api-usage.md) for the exact policy and tradeoffs.

## Known problems

- Some podcast clients don't accept feeds over HTTPS only. See [this upstream workaround](https://github.com/olaven/NRSS/issues/5#issuecomment-1488840679).
- The full archive of a series is crawled in the background after the first request, so a brand-new feed briefly shows only the latest ~50 episodes before growing to completion (depending on archive size and upstream availability).

## License

[AGPL-3.0](./LICENSE), same as upstream. Original project ([NRSS](https://github.com/olaven/nrss)) by [Olav Sundfør](https://github.com/olaven).
