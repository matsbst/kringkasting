# NRSS – RSS feeds for NRK's podcasts

A self-hostable web app that generates open, public RSS feeds for NRK's podcasts via their [API](https://psapi.nrk.no/documentation/), so you can listen to them in whatever podcast app you prefer.

This is a fork of [olaven/nrss](https://github.com/olaven/nrss), rebuilt to run on a [Cloudron](https://www.cloudron.io/) server (or anywhere Docker runs). Notable differences from upstream:

- Migrated from Fresh 1.x + twind to **Fresh 2 + Tailwind CSS 4** (vite-based build)
- Redesigned UI
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

To update a running instance to the latest image:

```sh
cloudron update --app nrss --image ghcr.io/matsbst/nrss:latest
```

Configuration (set automatically by `start.sh` / Cloudron):

| Env var                              | Purpose                                                                                                                                                                  |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `NRSS_KV_PATH`                       | Path of the Deno KV database used as a metadata cache (`/app/data/cache.sqlite3` on Cloudron). The data is a disposable cache; losing it just means refetching from NRK. |
| `APP_ORIGIN` / `CLOUDRON_APP_ORIGIN` | Public origin used for absolute URLs inside the RSS feeds. Falls back to the request origin.                                                                             |

## Running with plain Docker

```sh
docker build -t nrss .
docker run -p 8000:8000 -v nrss-data:/app/data nrss
```

## Known problems

- Some podcast clients don't accept feeds over HTTPS only. See [this upstream workaround](https://github.com/olaven/NRSS/issues/5#issuecomment-1488840679).
- Feeds only include the latest episodes of a podcast, not the entire archive (upstream [issue #8](https://github.com/olaven/NRSS/issues/8)). A background backlog fetcher is a planned improvement of this fork.

## License

[AGPL-3.0](./LICENSE), same as upstream. Original project by [Olav Sundfør](https://github.com/olaven).
