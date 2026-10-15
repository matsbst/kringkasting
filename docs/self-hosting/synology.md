# 📻 Kringkasting on Synology Container Manager

[← Self-hosting guide](../self-hosting.md)

Run the same Kringkasting container on your NAS. No Go installation, extra database or source build is needed.

**Validation status:** the Compose syntax and container behaviour are checked separately from NAS UI behaviour. These device steps are documentation-based, not verified on Synology Container Manager hardware. Button names can vary with the installed version. Check the [image access note](../self-hosting.md#image-access) before installation.

## Before you start

- A NAS model that supports Container Manager, with DSM 7.2 or newer. Install Container Manager from Package Center.
- A 64-bit Intel/AMD (`amd64`) or ARM (`arm64`) container host. Older 32-bit ARM models are not covered by the published image.
- A dedicated folder on the NAS for Kringkasting's data and an available host port, such as `8000`.
- A feed address reachable by the intended podcast app. Choose it before distributing subscriptions; see [networking](../self-hosting.md#-public-https-address).

## 📁 Create the application

1. In File Station, create a dedicated project folder, for example `/volume1/docker/kringkasting`, with an empty `data` subfolder. Your volume number may differ.
2. Open **Container Manager → Project → Create**. Name the project `kringkasting` and select that project folder.
3. Choose the source option to create `docker-compose.yml` (or upload a file containing the YAML below).
4. Replace both the example host path and `APP_ORIGIN` before starting. Review the summary and start the project. A Web Station portal is not required to use the published port.

```yaml
services:
  kringkasting:
    image: ghcr.io/matsbst/kringkasting:latest
    restart: unless-stopped
    ports:
      - "8000:8000"
    environment:
      APP_ORIGIN: "http://192.168.1.20:8000"
    volumes:
      - /volume1/docker/kringkasting/data:/app/data
```

`192.168.1.20` is an example NAS address. Replace it with yours, or set the stable public HTTPS origin if a reverse proxy is already configured. The example uses literal values instead of depending on the dashboard to load a `.env` file. If port `8000` is occupied, change the **left** port (for example `18000:8000`) and use that port in your LAN origin.

Create the folder before deploying and double-check the path. Mount only the `data` subfolder: the entrypoint recursively changes its ownership to UID/GID `1000:1000`, then runs the app without root. Do not mount a shared-folder root, add a `user:` override or assume `PUID`/`PGID` will change the user. The image does not read those variables.

If ownership changes or writes fail, inspect the container logs and the dedicated folder's ACLs/permissions. Do not change permissions on unrelated NAS shares. No privileged mode, host networking or Docker socket mount is required. Keep the live database on a local NAS folder, not a remotely mounted SMB/NFS share.

## 🎧 Check it works

Open `http://YOUR-NAS:8000` (or your chosen port). Check the container's logs/health status, search for a podcast and inspect the copied feed URL. Test subscribing in your intended podcast player.

A LAN-only address can work for direct-fetch players on your network, but apps using external feed servers may need public HTTPS. Adding `APP_ORIGIN` alone does not configure a reverse proxy, DNS or a certificate. Point a NAS reverse proxy at the published app port; do not use your NAS management page as the feed origin. Feed URLs must work without browser login prompts.

For the optional admin dashboard, add `ADMIN_TOKEN: "YOUR-LONG-RANDOM-TOKEN"` under `environment`, apply the configuration and open `/admin`. Leave it unset to keep admin disabled. Do not publish the token or the configured YAML.

## 🔄 Update

Back up first, and save the current image version and YAML. Use the platform's image pull/update and application recreation controls, retaining the same data-folder mapping. A restart alone does not download or apply a new image. Do not remove the data folder when cleaning/recreating the application.

If you manage this project over SSH and its Docker CLI provides Compose, run the reference [update commands](../self-hosting.md#-updating) from the actual project folder using the same file name and project name as the dashboard. Do not create an unrelated second project against the same data. The alternative is to keep the entire update in the dashboard; verify the new image ID afterwards.

## 💾 Back up and restore

For a backup without SSH:

1. Stop the application/project in the dashboard and wait until its container is fully stopped.
2. Copy the **whole `data` folder**, including any `.sqlite3-wal` and `.sqlite3-shm` files, to a separate backup location. Keep the YAML and image version too.
3. Restart the application. Copy the backup off the NAS as part of your backup routine.

Do not copy only the main database file while the app is running. For an online SQLite snapshot using the container terminal, follow the reference [backup procedure](../self-hosting.md#-backups), substituting the actual container name for plain-Docker commands.

To restore a stopped-directory backup:

1. Stop the current application and keep its existing data folder as a rollback copy.
2. Create a **new empty folder** and restore the entire backed-up directory into it. Do not overlay a different database's WAL/SHM files.
3. Change the YAML's host storage path to that restored folder, keeping `/app/data` as the container path and `APP_ORIGIN` unchanged. Recreate/start the application with a compatible saved image. Startup fixes ownership if the NAS permits it.
4. Check logs and health, then known episode GUIDs, oldest archived episodes and crawl progress. If available, use the container terminal to run `sqlite3 -readonly /app/data/kringkasting.sqlite3 "PRAGMA integrity_check; SELECT COUNT(*) FROM episodes;"` and compare with the backup's expected values.
5. Keep the previous folder until you have verified recovery. Never run both installations against one writable data folder.

If restoring a SQLite `.backup` file instead of a directory snapshot, place it as `kringkasting.sqlite3` in a new empty data folder. Do not add WAL/SHM files from the old live database to that standalone backup.

## Sources and limitations

- [Synology: Container Manager projects](https://kb.synology.com/en-us/DSM/help/ContainerManager/docker_project?version=7)
- [Synology: Compose project mechanism](https://help.synology.com/developer-guide/resource_acquisition/docker-project.html)
- [Homey vendor guide corroborating the Synology project-creation UI](https://support.homey.app/hc/en-us/articles/23975209649308-How-to-install-Homey-Self-Hosted-Server-on-Synology) — its app settings do not apply to Kringkasting.

The shared application contract is documented in the [Docker guide](../self-hosting.md). Actual NAS model compatibility, ACL behaviour, image updates and restore flows still need an on-device test before being advertised as certified support.
