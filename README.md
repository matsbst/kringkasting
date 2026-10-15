# 📻 Kringkasting

**NRK podcasts in the podcast app you choose.**

Kringkasting turns NRK's podcast catalog into open RSS feeds. Find a podcast, choose your app and subscribe—no Kringkasting account needed.

**[Find a podcast at kringkast.ing →](https://kringkast.ing)**

## 🎧 How it works

1. **Find your podcast.** Search for its name.
2. **Choose your app.** Use the subscribe button, or copy the RSS link into your preferred podcast player.
3. **Listen.** Your app follows the feed and picks up new episodes.

The subscribe chooser includes Apple Podcasts, Overcast, Pocket Casts, Castro, Downcast, Podcast Addict, AntennaPod and YouTube Music. Other players can use the RSS link if they support adding feeds by URL.

## ✨ What you get

- **Full episode archives:** the service fetches the available back catalog, beyond the latest page of episodes.
- **Season subscriptions:** subscribe to an individual season where NRK publishes one separately.
- **NRK's chapter markers:** available in supporting players when NRK provides them.
- **Your choice of app:** the website remembers your selection in your browser. Search and subscription links also work without JavaScript.
- **No media hosting:** audio and artwork come directly from NRK. Kringkasting provides the feeds and stores podcast metadata.
- **Your own instance, if you want one:** a small Go app in one container, with SQLite and no required external services.

## 💡 Why Kringkasting?

NRK makes its podcasts available through its own app. Kringkasting makes them accessible as RSS subscriptions, so you can listen in the app you prefer.

Kringkasting is independent of NRK. It uses [NRK's public API](https://psapi.nrk.no/documentation/) and does not select or rewrite the editorial content.

## 🏠 Run your own

Use the public instance at [kringkast.ing](https://kringkast.ing), or host the same app yourself. The container supports `amd64` and `arm64`, with no separate database to install.

For a quick local start with Docker Compose:

```sh
mkdir kringkasting
cd kringkasting
curl -fsSLO https://raw.githubusercontent.com/matsbst/kringkasting/main/compose.yaml
docker compose up -d
```

Open <http://localhost:8000>. The supplied Compose file retains the database in a persistent volume.

**[Self-hosting guide →](docs/self-hosting.md)** — reachable feed addresses, configuration, HTTPS, updates, backups and Cloudron installation.

## ℹ️ Good to know

- **Archives build in the background.** A new feed may initially contain only the latest episodes while older ones are fetched.
- **Availability comes from NRK.** Kringkasting cannot restore media that NRK no longer serves.
- **Some shows are stream-only.** These use HLS, which not every podcast app supports. The subscribe dialog flags stream-only shows.
- **Requests are cached and limited.** The service reuses metadata and adapts refresh frequency to avoid unnecessary requests to NRK. [How the API is used](docs/nrk-api-usage.md).

## 🤝 Questions and feedback

Found a problem or have an idea that helps get NRK podcasts into your app? [Open an issue](https://github.com/matsbst/kringkasting/issues).

Kringkasting began as a fork of [NRSS](https://github.com/olaven/nrss), created by [Olav Sundfør](https://github.com/olaven).

## 📄 License

[AGPL-3.0](LICENSE). If you run a modified version as a public service, you must offer its corresponding source code to users.
