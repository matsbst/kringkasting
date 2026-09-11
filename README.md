# NRSS – RSS feeds for NRK's podcasts

A self-hostable web app that generates open, public RSS feeds for NRK's podcasts via their [API](https://psapi.nrk.no/documentation/), so you can listen to them in whatever podcast app you prefer.

This is a fork of [olaven/nrss](https://github.com/olaven/nrss), rebuilt to run on a [Cloudron](https://www.cloudron.io/) server (or anywhere Docker runs). Notable differences from upstream:

- **Full episode archives**: a background crawler pages through each series' complete backlog, so feeds aren't limited to the latest ~20 episodes (fixes upstream [issue #8](https://github.com/olaven/NRSS/issues/8))
- Storage on plain **SQLite** (built-in `node:sqlite`) instead of Deno KV
- **Conditional GETs**: feeds answer `304 Not Modified` to polling podcast clients
- Feeds carry real enclosure byte sizes, `atom:link rel=self`, `language`, and use the registered `audio/mpeg` MIME type
- Migrated from Fresh 1.x + twind to **Fresh 2 + Tailwind CSS 4** (vite-based build)
- Redesigned UI with one-tap subscribe links (Apple Podcasts, Overcast, Pocket Casts, Castro, AntennaPod)
- Removed the Vipps donation integration
- All remote `https://` imports replaced with local code or `npm:`/`jsr:` packages
- Docker + Cloudron packaging, image built by GitHub Actions and published to ghcr.io

## Why?

NRK, Norway's government-funded public broadcaster, locks its podcasts into its own app instead of building on open standards like RSS. NRSS opens them back up.

## Local development

1. [Install Deno](https://docs.deno.com/runtime/getting_started/installation/) (2.x)
2. `deno install --allow-scripts`
3. `deno task dev`
4. Open [localhost:5173](http://localhost:5173)

Other tasks: `deno task check` (fmt, lint, types), `deno task test` (integration tests against NRK's live API), `deno task build` + `deno task start` (production build and serve).

## Deploying on Cloudron

The GitHub Actions workflow publishes an amd64 image to `ghcr.io/matsbst/nrss` on every push to `main`.

Install it as a [custom app](https://docs.cloudron.io/packaging/cli/) with the Cloudron CLI (run from a checkout of this repo, where `CloudronManifest.json` lives):

```sh
npm install -g cloudron
cloudron login my.example.com
cloudron install --image ghcr.io/matsbst/nrss:latest --location nrss
```

To update a running instance, use the commit-SHA tag (Cloudron does **not** re-pull `:latest` when the tag string is unchanged):

```sh
cloudron update --app nrss --image ghcr.io/matsbst/nrss:$(git rev-parse HEAD)
```

If the instance sits behind Cloudflare, see [docs/cloudflare.md](./docs/cloudflare.md) for the recommended edge-caching setup.

Configuration (set automatically by `start.sh` / Cloudron):

| Env var                              | Purpose                                                                                                                                                                         |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `NRSS_DB_PATH`                       | Path of the SQLite database holding podcast metadata and the crawled episode archives (`/app/data/nrss.sqlite3` on Cloudron). Losing it means archives get re-crawled from NRK. |
| `APP_ORIGIN` / `CLOUDRON_APP_ORIGIN` | Public origin used for absolute URLs inside the RSS feeds. Falls back to the request origin.                                                                                    |

## Running with plain Docker

```sh
docker build -t nrss .
docker run -p 8000:8000 -v nrss-data:/app/data nrss
```

## Known problems

- Some podcast clients don't accept feeds over HTTPS only. See [this upstream workaround](https://github.com/olaven/NRSS/issues/5#issuecomment-1488840679).
- The full archive of a series is crawled in the background after the first request, so a brand-new feed briefly shows only the latest ~50 episodes before growing to completion (usually within a few minutes).

## License

[AGPL-3.0](./LICENSE), same as upstream. Original project by [Olav Sundfør](https://github.com/olaven).
