import { page } from "fresh";
import { Head } from "fresh/runtime";
import { HttpError } from "fresh";
import { define } from "../utils.ts";
import { storage } from "../lib/storage.ts";
import { caching } from "../lib/caching.ts";
import { getBacklogStatus } from "../lib/backlog.ts";
import { enqueueBacklogCrawl } from "../lib/backlog.ts";
import { getStats, startedAt } from "../lib/stats.ts";
import { getCatalogSize } from "../lib/catalog.ts";
import { adminEnabled, isAuthenticated, tryLogin } from "../lib/admin-auth.ts";
import { isValidResourceId } from "../lib/utils.ts";

function noStore(response: Response): Response {
  response.headers.set("Cache-Control", "no-store");
  response.headers.set("X-Robots-Tag", "noindex");
  return response;
}

type Data =
  | { view: "login"; failed: boolean }
  | {
    view: "dashboard";
    stats: ReturnType<typeof getStats>;
    series: ReturnType<typeof storage.listSeriesOverview>;
    failedEpisodes: ReturnType<typeof storage.listFailedEpisodes>;
    episodeCount: number;
    dbBytes: number;
    backlog: ReturnType<typeof getBacklogStatus>;
    catalogSize: number | null;
    version: string;
    notice: string | null;
  };

function dashboardData(notice: string | null): Data {
  return {
    view: "dashboard",
    stats: getStats(),
    series: storage.listSeriesOverview(),
    failedEpisodes: storage.listFailedEpisodes(30),
    episodeCount: storage.countEpisodes(),
    dbBytes: storage.databaseSizeBytes(),
    backlog: getBacklogStatus(),
    catalogSize: getCatalogSize(),
    version: Deno.env.get("DENO_DEPLOYMENT_ID")?.slice(0, 10) ?? "dev",
    notice,
  };
}

export const handler = define.handlers({
  async GET(ctx) {
    if (!adminEnabled()) {
      throw new HttpError(404);
    }
    if (!(await isAuthenticated(ctx.req))) {
      return page({ view: "login", failed: false } satisfies Data);
    }
    const notice = new URL(ctx.req.url).searchParams.get("m");
    return page(dashboardData(notice));
  },

  async POST(ctx) {
    if (!adminEnabled()) {
      throw new HttpError(404);
    }
    const form = await ctx.req.formData();

    // login
    const token = form.get("token");
    if (typeof token === "string") {
      const headers = await tryLogin(ctx.req, token);
      if (!headers) {
        return page({ view: "login", failed: true } satisfies Data);
      }
      headers.set("Location", "/admin");
      return noStore(new Response(null, { status: 303, headers }));
    }

    if (!(await isAuthenticated(ctx.req))) {
      return page({ view: "login", failed: false } satisfies Data);
    }

    // admin actions (POST + SameSite=Strict cookie keeps this same-origin)
    const action = String(form.get("action") ?? "");
    const seriesId = String(form.get("seriesId") ?? "");
    let notice = "Ukjent handling";

    if (action === "gc") {
      const deleted = storage.deleteStaleSeries(90 * 24 * 60 * 60 * 1000);
      notice = `Rydding kjørt: ${deleted} serier fjernet`;
    } else if (isValidResourceId(seriesId)) {
      if (action === "refresh") {
        storage.expireSeries(seriesId);
        await caching.getSeries({ id: seriesId });
        notice = `${seriesId} oppdatert`;
      } else if (action === "recrawl") {
        storage.setBacklogState(seriesId, null, false);
        enqueueBacklogCrawl(seriesId);
        notice = `Arkiv-innhenting startet på nytt for ${seriesId}`;
      } else if (action === "delete") {
        storage.deleteSeriesById(seriesId);
        notice = `${seriesId} slettet`;
      }
    }

    const headers = new Headers({ Location: `/admin?m=${encodeURIComponent(notice)}` });
    return noStore(new Response(null, { status: 303, headers }));
  },
});

const formatTime = (date: Date) =>
  date.toLocaleString("nb-NO", { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" });

const formatBytes = (bytes: number) =>
  bytes > 1024 * 1024 ? `${(bytes / (1024 * 1024)).toFixed(1)} MB` : `${Math.round(bytes / 1024)} kB`;

export default define.page<typeof handler>(function Admin({ data }) {
  return (
    <>
      <Head>
        <title>Admin – Kringkasting</title>
        <meta name="robots" content="noindex" />
      </Head>

      {data.view === "login" ? <Login failed={data.failed} /> : <Dashboard data={data} />}
    </>
  );
});

function Login({ failed }: { failed: boolean }) {
  return (
    <section class="pt-20 pb-12 max-w-sm">
      <h1 class="text-3xl font-bold tracking-tight">Admin</h1>
      {failed && <p role="alert" class="mt-4 text-sm">Feil token, prøv igjen.</p>}
      <form method="post" class="mt-6 flex gap-2">
        <label class="sr-only" htmlFor="token">Admin-token</label>
        <input
          type="password"
          id="token"
          name="token"
          autocomplete="current-password"
          class="flex-1 h-11 rounded-lg border-2 border-edge dark:border-edge-dark bg-canvas dark:bg-canvas-dark px-3 focus:outline-none focus:border-ink dark:focus:border-ink-dark"
        />
        <button
          type="submit"
          class="rounded-lg bg-ink text-canvas dark:bg-ink-dark dark:text-canvas-dark text-sm font-semibold px-4 cursor-pointer"
        >
          Logg inn
        </button>
      </form>
    </section>
  );
}

function Dashboard({ data }: { data: Extract<Data, { view: "dashboard" }> }) {
  const incomplete = data.series.filter((serie) => !serie.backlogComplete).length;
  const upstreamRows = Object.entries(data.stats.upstream).filter(([, bucket]) => bucket.requests > 0);

  return (
    <div class="pt-12 pb-8">
      <div class="flex items-baseline justify-between flex-wrap gap-2">
        <h1 class="text-3xl font-bold tracking-tight">Admin</h1>
        <p class="text-sm text-ink-3 dark:text-ink-3-dark">
          versjon {data.version} · oppe siden {formatTime(startedAt)}
        </p>
      </div>

      {data.notice && (
        <p role="status" class="mt-4 rounded-lg bg-ink/5 dark:bg-ink-dark/8 px-3 py-2 text-sm">{data.notice}</p>
      )}

      <section class="mt-8 grid grid-cols-2 sm:grid-cols-4 gap-3">
        {[
          [
            String(data.series.length),
            data.catalogSize ? `serier fulgt (av ${data.catalogSize} hos NRK)` : "serier fulgt",
          ],
          [String(data.episodeCount), "episoder i arkiv"],
          [String(incomplete), "arkiv under innhenting"],
          [formatBytes(data.dbBytes), "database"],
        ].map(([value, label]) => (
          <div key={label} class="rounded-2xl bg-ink/5 dark:bg-ink-dark/8 p-4">
            <p class="text-3xl font-bold tabular-nums tracking-tight">{value}</p>
            <p class="mt-1 text-sm text-ink-2 dark:text-ink-2-dark">{label}</p>
          </div>
        ))}
      </section>

      <section class="mt-12">
        <h2 class="text-xl font-semibold">
          NRK-trafikk <span class="text-sm font-normal text-ink-3 dark:text-ink-3-dark">siden omstart</span>
        </h2>
        <div class="mt-4 grid grid-cols-2 sm:grid-cols-4 gap-3">
          {[
            [String(data.stats.upstreamTotal), "forespørsler til NRK"],
            [String(data.stats.upstreamErrors), "NRK-feil"],
            [`${data.stats.served.feeds200} / ${data.stats.served.feeds304}`, "feed 200 / 304"],
            [String(data.stats.served.rateLimited), "429 servert"],
          ].map(([value, label]) => (
            <div key={label} class="rounded-2xl bg-ink/5 dark:bg-ink-dark/8 p-4">
              <p class="text-2xl font-bold tabular-nums tracking-tight">{value}</p>
              <p class="mt-1 text-sm text-ink-2 dark:text-ink-2-dark">{label}</p>
            </div>
          ))}
        </div>
        {upstreamRows.length > 0 && (
          <table class="mt-4 w-full text-sm">
            <thead>
              <tr class="text-left text-ink-2 dark:text-ink-2-dark">
                <th class="py-1.5 font-medium">Kategori</th>
                <th class="py-1.5 font-medium text-right">Forespørsler</th>
                <th class="py-1.5 font-medium text-right">Feil</th>
              </tr>
            </thead>
            <tbody>
              {upstreamRows.map(([category, bucket]) => (
                <tr key={category} class="border-t border-line dark:border-line-dark">
                  <td class="py-1.5">{category}</td>
                  <td class="py-1.5 text-right tabular-nums">{bucket.requests}</td>
                  <td class="py-1.5 text-right tabular-nums">{bucket.errors}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <section class="mt-12">
        <h2 class="text-xl font-semibold">Helse</h2>
        <p class="mt-3 text-sm text-ink-2 dark:text-ink-2-dark">
          Innhentingskø: {data.backlog.queueLength} {data.backlog.running ? "(aktiv)" : "(inaktiv)"}
          {data.stats.lastGc &&
            ` · siste rydding ${formatTime(data.stats.lastGc.at)} (${data.stats.lastGc.deleted} fjernet)`}
        </p>
        {data.backlog.events.length > 0 && (
          <ul class="mt-3 space-y-1 text-sm text-ink-2 dark:text-ink-2-dark">
            {data.backlog.events.slice(0, 8).map((event, index) => (
              <li key={index}>
                <span class="tabular-nums text-ink-3 dark:text-ink-3-dark">{formatTime(event.at)}</span> {event.message}
              </li>
            ))}
          </ul>
        )}
        {data.failedEpisodes.length > 0 && (
          <>
            <h3 class="mt-6 font-semibold">Episoder i backoff ({data.failedEpisodes.length})</h3>
            <table class="mt-2 w-full text-sm">
              <thead>
                <tr class="text-left text-ink-2 dark:text-ink-2-dark">
                  <th class="py-1.5 font-medium">Serie</th>
                  <th class="py-1.5 font-medium">Episode</th>
                  <th class="py-1.5 font-medium text-right">Forsøk</th>
                  <th class="py-1.5 font-medium text-right">Neste forsøk</th>
                </tr>
              </thead>
              <tbody>
                {data.failedEpisodes.map((failed) => (
                  <tr key={failed.seriesId + failed.episodeId} class="border-t border-line dark:border-line-dark">
                    <td class="py-1.5">{failed.seriesId}</td>
                    <td class="py-1.5 truncate max-w-40">{failed.episodeId}</td>
                    <td class="py-1.5 text-right tabular-nums">{failed.attempts}</td>
                    <td class="py-1.5 text-right tabular-nums">{formatTime(failed.nextRetryAt)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
      </section>

      <section class="mt-12">
        <div class="flex items-baseline justify-between">
          <h2 class="text-xl font-semibold">Serier ({data.series.length})</h2>
          <form method="post">
            <input type="hidden" name="action" value="gc" />
            <button
              type="submit"
              class="text-sm underline underline-offset-2 text-ink-2 dark:text-ink-2-dark hover:text-ink dark:hover:text-ink-dark cursor-pointer"
            >
              Kjør rydding nå
            </button>
          </form>
        </div>
        <table class="mt-3 w-full text-sm">
          <thead>
            <tr class="text-left text-ink-2 dark:text-ink-2-dark">
              <th class="py-1.5 font-medium">Serie</th>
              <th class="py-1.5 font-medium text-right">Episoder</th>
              <th class="py-1.5 pl-5 font-medium">Arkiv</th>
              <th class="py-1.5 font-medium">Sist hentet</th>
              <th class="py-1.5 font-medium text-right">Handlinger</th>
            </tr>
          </thead>
          <tbody>
            {data.series.map((serie) => (
              <tr key={serie.id} class="border-t border-line dark:border-line-dark">
                <td class="py-2">
                  <a href={`/api/feeds/${serie.id}`} class="underline underline-offset-2">{serie.title}</a>
                </td>
                <td class="py-2 text-right tabular-nums">{serie.episodeCount}</td>
                <td class="py-2 pl-5">{serie.backlogComplete ? "komplett" : "pågår"}</td>
                <td class="py-2 tabular-nums">{formatTime(serie.lastFetchedAt)}</td>
                <td class="py-2 text-right whitespace-nowrap">
                  {[["refresh", "Oppdater"], ["recrawl", "Re-crawl"], ["delete", "Slett"]].map(([action, label]) => (
                    <form key={action} method="post" class="inline">
                      <input type="hidden" name="action" value={action} />
                      <input type="hidden" name="seriesId" value={serie.id} />
                      <button
                        type="submit"
                        class="ml-3 text-ink-2 dark:text-ink-2-dark underline underline-offset-2 hover:text-ink dark:hover:text-ink-dark cursor-pointer"
                      >
                        {label}
                      </button>
                    </form>
                  ))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </div>
  );
}
