# Kringkasting – RSS feeds for NRK's podcasts

**[kringkast.ing](https://kringkast.ing)** — a self-hostable web app that generates open, public RSS feeds for NRK's podcasts via their [API](https://psapi.nrk.no/documentation/), so you can listen to them in whatever podcast app you prefer.

Kringkasting started as a fork of [olaven/nrss](https://github.com/olaven/nrss). Notable differences from upstream:

- **Full episode archives**: a background crawler pages through each series' complete backlog, so feeds aren't limited to the latest ~20 episodes (fixes upstream [issue #8](https://github.com/olaven/NRSS/issues/8))
- **Per-season feeds**: NRK publishes many standalone shows as seasons of umbrella podcasts (e.g. Radiodokumentaren's titles); each gets its own feed at `/api/feeds/{serie}/sesong/{sesong}` with its own title and artwork, mirroring NRK's structure
- Storage on plain **SQLite** (built-in `node:sqlite`) — a single embedded file, no external database
- **Conditional GETs**: feeds answer `304 Not Modified` to polling clients, and send CDN-friendly caching headers
- Feeds carry real enclosure byte sizes, `atom:link rel=self`, `language`, and the registered `audio/mpeg` MIME type; stream-only shows are handled honestly
- Migrated from Fresh 1.x + twind to **Fresh 2 + Tailwind CSS 4** (vite-based build)
- Redesigned UI with instant search and one-tap subscribe (Apple Podcasts, Overcast, Pocket Casts, Castro, Downcast, Podcast Addict, AntennaPod, YouTube Music — your choice is remembered)
- Per-client rate limiting, a global upstream concurrency cap, and security headers
- Optional admin dashboard (`/admin`) and optional server-side error reporting (Sentry protocol / self-hosted [Bugsink](https://www.bugsink.com/))
- Dockerized, image built and published by GitHub Actions

## Why?

NRK, Norway's government-funded public broadcaster, locks its podcasts into its own app instead of building on open standards like RSS. Kringkasting opens them back up.

## Self-hosting

Kringkasting is a single long-running container with an embedded SQLite database — **no external database or services required.**

### Requirements

- A container runtime (Docker, Podman, or a platform that runs OCI images)
- A **persistent volume** mounted at `/app/data` (holds the podcast metadata and crawled archives)
- ~384 MB RAM, one HTTP port (the app listens on `8000`)
- Outbound HTTPS to `psapi.nrk.no` and NRK's media CDNs

The database is a _rebuildable cache_ — if you lose the volume the app simply re-crawls from NRK — but keep it so archives persist and you stay a light consumer of NRK's API.

### Quick start (Docker Compose)

```sh
curl -O https://raw.githubusercontent.com/matsbst/kringkasting/main/compose.yaml
# optionally: create a .env with APP_ORIGIN / ADMIN_TOKEN (see .env.example)
docker compose up -d
```

Then open <http://localhost:8000>. The [`compose.yaml`](./compose.yaml) uses the prebuilt image, a named volume, a healthcheck, and `restart: unless-stopped`.

### Quick start (plain Docker)

```sh
docker run -d --name kringkasting \
  -p 8000:8000 \
  -v kringkasting-data:/app/data \
  -e APP_ORIGIN=https://kringkasting.example.com \
  --restart unless-stopped \
  ghcr.io/matsbst/kringkasting:latest
```

### Configuration

All configuration is via environment variables (see [`.env.example`](./.env.example)):

| Env var                          | Purpose                                                                                                                                                                                        |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `APP_ORIGIN`                     | **Recommended.** Public origin (scheme + host) of your instance, used for absolute URLs in feeds and page metadata. Set it behind a proxy so URLs aren't built from the internal request Host. |
| `ADMIN_TOKEN`                    | Enables the admin dashboard at `/admin` (stats, NRK-traffic counters, archive health, per-series actions). Unset = disabled. Use a long random string.                                         |
| `SENTRY_DSN`                     | Optional. Sentry-protocol DSN (works with self-hosted Bugsink) for server-side error reporting, scrubbed of IP/query/body. Unset = disabled.                                                   |
| `UMAMI_SRC` / `UMAMI_WEBSITE_ID` | Optional. Load a cookieless [Umami](https://umami.is) analytics script (script URL + website id). Both must be set to enable; point them at your own Umami instance.                           |
| `KRINGKASTING_DB_PATH`           | SQLite database path. Defaults to `/app/data/kringkasting.sqlite3`; change only if you mount the volume elsewhere.                                                                             |
| `CLOUDRON_APP_ORIGIN`            | Set automatically on Cloudron; used as `APP_ORIGIN` if the latter is unset.                                                                                                                    |

### Behind a reverse proxy

Terminate TLS at a proxy in front of the container and forward to port 8000. Set `APP_ORIGIN` to your public URL. Example [Caddy](https://caddyserver.com/) config (automatic HTTPS):

```
kringkasting.example.com {
    reverse_proxy localhost:8000
}
```

For a CDN such as Cloudflare (edge-caching the feeds), see [docs/cloudflare.md](./docs/cloudflare.md).

### Updating

```sh
docker compose pull && docker compose up -d   # Compose
# or, plain Docker:
docker pull ghcr.io/matsbst/kringkasting:latest && docker restart kringkasting
```

### Backups

Back up the `/app/data` volume to preserve crawled archives. It's not critical — losing it triggers a re-crawl from NRK — but a backup avoids re-fetching and keeps you a light API consumer. Pin a specific image (`ghcr.io/matsbst/kringkasting:<git-sha>`) instead of `:latest` if you want reproducible deploys.

### Install on Cloudron

Kringkasting ships a `CloudronManifest.json`, so it installs as a [custom Cloudron app](https://docs.cloudron.io/packaging/cli/):

```sh
npm install -g cloudron
cloudron login my.example.com
cloudron install --image ghcr.io/matsbst/kringkasting:latest --location kringkasting
```

Cloudron provides the persistent `/app/data` and sets `CLOUDRON_APP_ORIGIN` automatically.

### Being kind to NRK

The service is a deliberately light, adaptive consumer of NRK's open API — subscriber polling never reaches NRK, refresh cadence scales with how actively a show publishes, and audio/artwork is served directly from NRK's CDNs. [docs/nrk-api-usage.md](./docs/nrk-api-usage.md) documents every endpoint used and the load-limiting measures.

## Local development

1. [Install Deno](https://docs.deno.com/runtime/getting_started/installation/) (2.x)
2. `deno install --allow-scripts`
3. `deno task dev`
4. Open [localhost:5173](http://localhost:5173)

Other tasks: `deno task check` (fmt, lint, types), `deno task test` (offline tests; `deno task test:live` checks NRK's live API), `deno task build` + `deno task start`. To build the container image yourself: `docker build -t kringkasting .`

## Known problems

- Some podcast clients don't accept feeds over HTTPS only. See [this upstream workaround](https://github.com/olaven/NRSS/issues/5#issuecomment-1488840679).
- A brand-new feed shows only the latest ~50 episodes until the background crawl completes (usually a few minutes).
- A few NRK radio programs are offered only as HLS streams, not downloadable files; those play in Apple Podcasts but not most other apps, and the subscribe dialog says so.

## License

[AGPL-3.0](./LICENSE), same as upstream. Original project ([NRSS](https://github.com/olaven/nrss)) by [Olav Sundfør](https://github.com/olaven). If you run a modified version as a public service, the AGPL requires you to offer its source to your users.
