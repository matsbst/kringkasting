# Cloudflare configuration for Kringkasting

The app sends CDN-friendly headers (`Cache-Control: public, max-age=1800, s-maxage=3600`, `ETag`, `Last-Modified`), but Cloudflare needs a little configuration to make use of them. With the steps below, nearly all feed polls are served from Cloudflare's edge and your origin only sees roughly one revalidation per feed per hour.

An edge revalidation does not necessarily trigger a request to NRK. The application checks each show's own refresh window: 3 hours, 12 hours, 3 days, or 7 days depending on its newest episode, with ±15% jitter. Refreshes and retries are request-driven; there is no timer polling idle shows. See [NRK API usage](nrk-api-usage.md) for checkpoint recovery, persistent retry deadlines, and indefinite archive retention.

The path rule below also covers chapter endpoints. Successful chapter responses and confirmed missing episodes advertise a one-day browser/edge TTL (`max-age=86400, s-maxage=86400`); the application keeps their results in SQLite for seven days. Keep the rule configured to respect response headers rather than forcing a single TTL on all feed paths.

## 1. Cache Rule for feeds (the important one)

By default Cloudflare only caches responses by file extension, so `/api/feeds/...` (no extension) always hits your origin.

**Dashboard → your zone → Caching → Cache Rules → Create rule:**

- **Rule name:** `Kringkasting feeds`
- **When incoming requests match:** Custom filter expression
  - Field `Hostname` equals `kringkast.ing`
  - AND field `URI Path` starts with `/api/feeds/`
- **Then:**
  - **Cache eligibility:** Eligible for cache
  - **Edge TTL:** Use cache-control header if present (the app sends `s-maxage=3600`)
  - **Browser TTL:** Respect origin

Cloudflare will revalidate its copy against the app's `ETag` when the edge TTL expires.

## 2. Don't let bot protection break podcast apps

Podcast clients look exactly like bots. If they get challenged, subscribers see broken feeds.

- **Free plan:** leave **Bot Fight Mode OFF** for this zone (Security → Bots). It cannot be selectively disabled per path.
- **Paid plans:** if you use bot management, add a WAF custom rule that **skips** bot checks for `URI Path starts with /api/feeds/`.

Also make sure no managed challenge / security rule covers `/api/*`.

## 3. Tiered Cache (free, one toggle)

**Caching → Tiered Cache → Smart Tiered Caching: On.** Fewer Cloudflare data centers contact your origin directly.

## 4. Serve stale on errors

**Caching → Configuration → Always Online: On.** Combined with the app's own serve-stale-on-NRK-outage behavior, feeds keep working through hiccups.

## Notes

- Static assets (`/assets/*.css|js`, `/fonts/*.woff2`) are cached by Cloudflare automatically (default cached extensions); the app marks fonts `immutable` and vite content-hashes the assets, so no purging is ever needed.
- After deploying a new app version, feeds may be served from the edge for up to an hour before the new XML shows. That is by design; purge the cache manually in the dashboard if you ever need it immediately.
