# 🏠 Self-hosting Kringkasting

Run your own instance with Docker Compose, plain Docker or Cloudron. Kringkasting runs as one container with SQLite storage; no external database, account or additional service is required. The published-image installation needs no Go installation or source build.

[← Back to the README](../README.md)

Choose your setup: **[Docker Compose](#-docker-compose)** · **[Synology](self-hosting/synology.md)** · **[QNAP](self-hosting/qnap.md)** · **[Cloudron](#-cloudron)**.

For recovery, see **[backups](#-backups)** and **[restore](#-restore-from-a-database-backup)**.

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

### Storage and permissions

The supplied Compose file uses a named volume. Docker prefixes its name with the Compose project name, so keep the installation folder/project name stable. Changing it can create a new empty volume that looks like a lost archive. Do not use `docker compose down -v` when updating: `-v` removes named volumes.

For a NAS or a visible host folder, replace the service's volume mapping with a dedicated directory, for example:

```yaml
    volumes:
      - /absolute/path/to/kringkasting/data:/app/data
```

Create that folder first and replace the example with its real absolute path. Mount only Kringkasting's data folder, not a shared-folder root: startup recursively changes ownership to UID/GID `1000:1000` before running as that user. Keep the default startup user so that step can run. `PUID` and `PGID` are not supported. If a platform requires a non-root startup user, preconfigure its data-directory permissions for that numeric user instead.

Keep the database on local storage attached to the machine running the container. Do not mount a remote SMB/NFS share as its live database directory; [SQLite WAL relies on same-host shared memory](https://www.sqlite.org/wal.html).

### Check the installation

```sh
docker compose ps
docker compose logs --tail=100 kringkasting
docker compose exec -T kringkasting /app/code/kringkasting healthcheck
```

Then search for a podcast, copy its feed link and check that it starts with the address you intended. Subscribe from a real podcast app, not just the server's own browser. Restart the service and confirm the same feed and archive data remain available:

```sh
docker compose restart kringkasting
```

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

Use a stable hostname before sharing subscriptions; moving the database does not move subscribers to a different feed address. A LAN address works only for clients that can reach your LAN. Some apps fetch through their own servers and need a publicly reachable address. A VPN does not automatically make those external servers able to reach it.

Terminate TLS at a proxy in front of the container and forward to port 8000. Feed and chapter URLs must not require an interactive login or browser challenge. Set `APP_ORIGIN` to your public URL. Example [Caddy](https://caddyserver.com/) config (automatic HTTPS):

```
kringkasting.example.com {
    reverse_proxy localhost:8000
}
```

This example assumes Caddy runs directly on the same host. A proxy in another container must reach Kringkasting through its container network and port `8000`; `localhost` inside the proxy refers to the proxy itself. With a host-based proxy, you can restrict the published port to `127.0.0.1:8000:8000`; leave the LAN binding only when LAN clients or your NAS proxy need it.

For a CDN such as Cloudflare (edge-caching the feeds), see [Cloudflare configuration](cloudflare.md).

## 🔄 Updating

Back up your database before an update. Save your Compose file, `.env` and the currently running image ID as well:

```sh
docker compose images kringkasting
```

Keep an immutable image reference (a digest or git-SHA tag) if you need a reproducible rollback. Before updating, you can tag the current image locally: get its image ID from the command above and run `docker tag IMAGE_ID kringkasting:before-update`, replacing `IMAGE_ID` with that value. A local tag stays on this Docker host; it is not an off-host backup.

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

Restarting an existing container does not switch it to a newly downloaded image. Keep a copy of your configuration.

### Image versions

| Tag | Follows |
| --- | --- |
| `:latest` | every change merged to `main`, as soon as it is built |
| `:2` | the newest 2.x release |
| `:2.0` | the newest 2.0.x release |
| `:2.0.0` | exactly that release, never moved |
| `:<git-sha>` | exactly that build, never moved |

`:latest` is what the public instance runs. For updates only when a release is published, use `:2` instead. Each release is listed with its changes under [Releases](https://github.com/matsbst/kringkasting/releases).

### Roll back an image

Stop the new container, set its `image:` to your saved image reference (or `kringkasting:before-update` on the original host), and run `docker compose up -d` with the same storage and configuration. Check the release notes for database compatibility before starting an older version. Do not restore an old database just to change the image: that discards metadata saved since the backup.

## 💾 Backups

The database contains accumulated episode metadata and archive progress, not audio or video files. Keep backups: metadata for episodes no longer exposed by NRK may not be recoverable by crawling again.

The image includes `sqlite3` for consistent backups while the app runs.

With Compose, from your installation folder:

```sh
mkdir -p backups
docker compose exec -T kringkasting sqlite3 /app/data/kringkasting.sqlite3 ".backup /app/data/kringkasting-backup.sqlite3"
docker compose cp kringkasting:/app/data/kringkasting-backup.sqlite3 ./backups/kringkasting-backup.sqlite3
```

Each run replaces that backup filename. Archive it under a dated name if you want multiple recovery points. Copy the downloaded backup off the app host and keep the Compose file, `.env` and image version separately. Configuration may contain secrets; keep it private.

Check the downloaded backup using the same image's SQLite tool. This does not start another application server:

```sh
docker compose run --rm --no-deps --entrypoint sqlite3 \
  -v "$(pwd)/backups:/backups:ro" \
  kringkasting -readonly "file:/backups/kringkasting-backup.sqlite3?immutable=1" \
  "PRAGMA integrity_check; SELECT COUNT(*) AS episodes FROM episodes;"
```

The integrity result must be `ok`. Record the episode count for the restore check. Integrity alone does not prove every expected episode was saved; compare the count and, where needed, known episode IDs and crawl checkpoints.

`immutable=1` lets SQLite verify this completed, standalone backup on a read-only mount without creating WAL sidecar files. Use it only for the backup snapshot, never for a database the app is writing to.

For a container created with plain Docker, use `docker exec kringkasting` and `docker cp kringkasting:…` in place of the corresponding Compose commands. Use its image with `docker run --rm --entrypoint sqlite3` and a read-only mount of the backup folder for verification.

Do not copy only `kringkasting.sqlite3` while the app is running: recent writes may be in `-wal`. Use SQLite's backup mechanism, or stop the app completely before copying the **entire data directory**, including any `-wal` and `-shm` files. NAS snapshots are not a substitute for a rehearsed database restore. [SQLite backup documentation](https://www.sqlite.org/backup.html).

## ♻️ Restore from a database backup

This procedure uses the supplied named-volume Compose setup and the default database path. It restores to a **new volume**, keeping the old data intact. For NAS bind mounts, follow the matching [Synology](self-hosting/synology.md#-back-up-and-restore) or [QNAP](self-hosting/qnap.md#-back-up-and-restore) guide.

1. Put the backup at `./backups/kringkasting-backup.sqlite3` in the installation folder. Verify it with the read-only command above. Use the saved image version that created the backup, or a documented compatible version.
2. Create a fresh restore volume. Choose another name everywhere below if `kringkasting-restored` was used previously:

```sh
docker volume create kringkasting-restored
```

3. Copy the verified backup into it. The guard refuses a nonempty destination, so an existing database or stale WAL files cannot be mixed into the restore. This command runs a maintenance shell, not the app:

```sh
docker compose run --rm --no-deps --entrypoint sh \
  -v kringkasting-restored:/restore \
  -v "$(pwd)/backups:/backups:ro" \
  kringkasting -ec '
    test -z "$(ls -A /restore)" || { echo "Restore volume must be empty" >&2; exit 1; }
    cp /backups/kringkasting-backup.sqlite3 /restore/kringkasting.sqlite3
    chown 1000:1000 /restore/kringkasting.sqlite3
  '
```

Only continue if that command succeeds. Do not run the application against this volume until it is ready to replace the original instance.

4. Create `compose.restore.yaml` in the installation folder:

```yaml
volumes:
  kringkasting-data:
    external: true
    name: kringkasting-restored
```

5. Stop the old app, then recreate it using the restored volume:

```sh
docker compose stop kringkasting
docker compose -f compose.yaml -f compose.restore.yaml up -d
```

6. Check the restored instance:

```sh
docker compose -f compose.yaml -f compose.restore.yaml exec -T kringkasting \
  sqlite3 -readonly /app/data/kringkasting.sqlite3 \
  "PRAGMA integrity_check; SELECT COUNT(*) AS episodes FROM episodes;"
docker compose -f compose.yaml -f compose.restore.yaml exec -T kringkasting \
  /app/code/kringkasting healthcheck
```

Expect `ok` and the saved episode count before any new feed requests trigger updates. Check a known feed's GUIDs, oldest saved episodes and archive progress as well. Keep `APP_ORIGIN` unchanged so existing feed addresses remain valid. The application healthcheck does not verify your archive contents.

**After switching, include both `-f` options in every Compose command**, including backups, updates and logs, or merge the restored volume declaration into your main Compose file. Using only the original file can switch the app back to its old volume. Keep the old volume until recovery is confirmed; do not run two app instances against one writable database.

To return to the original volume, stop the restored service using both files, then run `docker compose -f compose.yaml up -d`. This also returns to the original volume's older state. The separate volumes are recovery points, not synchronized copies.

To rehearse without touching the running app, perform the procedure in a separate installation folder/project, use a new restore-volume name and a different host port, and make no live feed/search requests. The `/personvern` healthcheck does not fetch NRK data. Do not expose the rehearsal's feeds to subscribers.

Locally rehearsed with a source-built image and the saved database fixture: live backup, integrity check, fresh-volume restore, rejection of a nonempty destination, full database comparison, restart persistence and return to the original volume all passed. This validates the Docker recovery procedure; NAS dashboard steps still need device testing.

## ☁️ Cloudron

Kringkasting ships a `CloudronManifest.json`, so it installs as a [custom Cloudron app](https://docs.cloudron.io/packaging/cli/):

```sh
npm install -g cloudron
cloudron login my.example.com
cloudron install --image ghcr.io/matsbst/kringkasting:latest --location kringkasting
```

Cloudron provides the persistent `/app/data` and sets `CLOUDRON_APP_ORIGIN` automatically.

## Troubleshooting

### Image access

`unauthorized` or `denied` when pulling from GHCR means the image is not accessible with the current registry credentials. Verify that the package is published and public before following the account-free installation path. If the maintainer intentionally makes it private, registry login and read access are required; do not work around it with another image. No application settings or NAS permissions can fix a registry access failure.

### Runtime problems

- **The page won't open:** check the published port and inspect logs with `docker compose logs --tail=100 kringkasting`.
- **A phone or podcast app can't load a feed:** check that `APP_ORIGIN` is reachable from that client, including any server-side feed fetcher it uses.
- **Older episodes are missing on a new feed:** allow the background archive crawl to progress. Temporary upstream failures are retried on subsequent requests after a backoff.
- **Playback fails in one app:** some NRK programs are available only as HLS streams, which not every podcast player supports.

For a reproducible problem, [open an issue](https://github.com/matsbst/kringkasting/issues) with the image version, installation method and symptoms. Do not include admin tokens or other secrets.
