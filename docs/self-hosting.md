# 🏠 Self-hosting Kringkasting

Run your own instance with Docker Compose, plain Docker or Cloudron. Kringkasting runs as one container with SQLite storage; no external database, account or additional service is required. No Go installation or source build is needed.

[← Back to the README](../README.md)

## Before you start

- A container runtime and an `amd64` or `arm64` Linux host. Images are published for both architectures.
- Persistent storage mounted at `/app/data` for episode metadata, feed caches and archive progress.
- An available HTTP port; the container listens on `8000`.
- Outbound HTTPS access to NRK's API and media servers.
- Memory allowance: roughly 50 MB idle and around 150 MB with a few dozen large archives, depending on usage. The image sets a `256MiB` Go soft memory limit.

For subscriptions, use an address your podcast app can reach. `localhost` works only on the machine running the app; it won't point a phone at your server. Some podcast apps fetch feeds through their own servers, so a LAN-only address may not work with them.

## 🐳 Docker Compose

Create a folder for your installation and download the Compose file into it:

```sh
mkdir kringkasting
cd kringkasting
curl -fsSLO https://raw.githubusercontent.com/matsbst/kringkasting/main/compose.yaml
```

For a public instance, create a `.env` file in the same folder and set its address:

```dotenv
APP_ORIGIN=https://podcasts.example.com
```

Then start the app:

```sh
docker compose up -d
```

Open `http://YOUR-SERVER:8000`, or <http://localhost:8000> when running locally. For a public HTTPS address, configure a reverse proxy as described below. Setting `APP_ORIGIN` identifies that address; it does not configure DNS or HTTPS for you.

The Compose file creates a persistent volume and restarts the app automatically unless you stop it. Keep that volume when updating or reinstalling.

## Docker without Compose

```sh
docker run -d --name kringkasting \
  -p 8000:8000 \
  -v kringkasting-data:/app/data \
  -e APP_ORIGIN=https://podcasts.example.com \
  --restart unless-stopped \
  ghcr.io/matsbst/kringkasting:latest
```

Replace `APP_ORIGIN` with your instance's reachable address. This example uses a named volume, `kringkasting-data`, to retain data when the container is replaced.

## ⚙️ Configuration

All configuration is via environment variables (see [`.env.example`](../.env.example)):

| Env var                          | Purpose                                                                                                                                                                                        |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `APP_ORIGIN`                     | **Recommended.** Public origin (scheme + host) of your instance, used for absolute URLs in feeds and page metadata. Set it behind a proxy so URLs aren't built from the internal request Host. |
| `ADMIN_TOKEN`                    | Enables the admin dashboard at `/admin` (stats, NRK-traffic counters, archive health, per-series actions). Unset = disabled. Use a long random string (at least 8 characters).                 |
| `SENTRY_DSN`                     | Optional. Sentry-protocol DSN (works with self-hosted Bugsink) for server-side error reporting, scrubbed of IP/query/body. Unset = disabled.                                                   |
| `UMAMI_SRC` / `UMAMI_WEBSITE_ID` | Optional. Load a cookieless [Umami](https://umami.is) analytics script (script URL + website id). Both must be set to enable; point them at your own Umami instance.                           |
| `KRINGKASTING_DB_PATH`           | SQLite database path. Defaults to `/app/data/kringkasting.sqlite3`; change only if you mount the volume elsewhere.                                                                             |
| `CLOUDRON_APP_ORIGIN`            | Set automatically on Cloudron; used as `APP_ORIGIN` if the latter is unset.                                                                                                                    |
| `GOMEMLIMIT`                     | Optional. Soft memory limit for the Go runtime; the image defaults to `256MiB`. Raise it if you give the container more memory and follow very many large archives.                           |

With the supplied Compose file, set `APP_ORIGIN`, `ADMIN_TOKEN`, `SENTRY_DSN` and the Umami settings in `.env`. To override `KRINGKASTING_DB_PATH` or `GOMEMLIMIT`, also add the variable to the service's `environment` section in `compose.yaml`; placing it in `.env` alone does not pass it into the container.

To enable the optional admin dashboard, set a long random `ADMIN_TOKEN` and open `/admin`. Leave it unset to keep the dashboard disabled.

## 🌐 Public HTTPS address

Terminate TLS at a proxy in front of the container and forward to port 8000. Set `APP_ORIGIN` to your public URL. Example [Caddy](https://caddyserver.com/) config (automatic HTTPS):

```
kringkasting.example.com {
    reverse_proxy localhost:8000
}
```

For a CDN such as Cloudflare (edge-caching the feeds), see [Cloudflare configuration](cloudflare.md).

## 🔄 Updating

Back up your database before an update.

For Compose, run these commands from your installation folder:

```sh
docker compose pull
docker compose up -d
```

For the plain Docker example above, pull the image and recreate the container using the same volume and configuration:

```sh
docker pull ghcr.io/matsbst/kringkasting:latest
docker stop kringkasting
docker rm kringkasting
# Run your original docker run command again with the same volume and settings.
```

Restarting an existing container does not switch it to a newly downloaded image. Keep a copy of your configuration. To select a specific release build, use `ghcr.io/matsbst/kringkasting:<git-sha>` instead of `:latest`.

## 💾 Backups

The database contains accumulated episode metadata and archive progress, not audio or video files. Keep backups: metadata for episodes no longer exposed by NRK may not be recoverable by crawling again.

The image includes `sqlite3` for consistent backups while the app runs.

With Compose:

```sh
docker compose exec -T kringkasting sqlite3 /app/data/kringkasting.sqlite3 ".backup /app/data/backup.sqlite3"
docker compose cp kringkasting:/app/data/backup.sqlite3 ./kringkasting-backup.sqlite3
```

With the plain Docker example:

```sh
docker exec kringkasting sqlite3 /app/data/kringkasting.sqlite3 ".backup /app/data/backup.sqlite3"
docker cp kringkasting:/app/data/backup.sqlite3 ./kringkasting-backup.sqlite3
```

Store the downloaded backup somewhere separate from the app's data volume. Backups preserve metadata; media availability still depends on NRK.

## ☁️ Cloudron

Kringkasting ships a `CloudronManifest.json`, so it installs as a [custom Cloudron app](https://docs.cloudron.io/packaging/cli/):

```sh
npm install -g cloudron
cloudron login my.example.com
cloudron install --image ghcr.io/matsbst/kringkasting:latest --location kringkasting
```

Cloudron provides the persistent `/app/data` and sets `CLOUDRON_APP_ORIGIN` automatically.

## Troubleshooting

- **The page won't open:** check the published port and inspect logs with `docker compose logs --tail=100 kringkasting`.
- **A phone or podcast app can't load a feed:** check that `APP_ORIGIN` is reachable from that client, including any server-side feed fetcher it uses.
- **Older episodes are missing on a new feed:** allow the background archive crawl to progress. Temporary upstream failures are retried on subsequent requests after a backoff.
- **Playback fails in one app:** some NRK programs are available only as HLS streams, which not every podcast player supports.

For a reproducible problem, [open an issue](https://github.com/matsbst/kringkasting/issues) with the image version, installation method and symptoms. Do not include admin tokens or other secrets.
