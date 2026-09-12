import { nrkRadio } from "./nrk/nrk.ts";
import { storage } from "./storage.ts";
import { canRetry, clearRetry, deferRetry } from "./retry.ts";

/**
 * Background crawler that pages through a series' complete episode
 * archive after it has been requested for the first time, so feeds grow
 * beyond the ~50 episodes NRK returns per catalog page.
 *
 * Deliberately gentle with NRK: one series at a time, sequential pages
 * with a pause in between, and progress is checkpointed per page in the
 * `backlog_cursor` column so a restart resumes where it left off.
 */

const PAGE_DELAY_MS = 1_000;
/** hard stop per series per crawl round, to bound worst-case runtime */
const MAX_PAGES_PER_RUN = 200;

const queue: string[] = [];
const queued = new Set<string>();
let running = false;
let active: Promise<void> | null = null;

/** ring buffer of recent crawl events for the admin dashboard */
const events: { at: Date; message: string }[] = [];
function recordEvent(message: string) {
  events.push({ at: new Date(), message });
  if (events.length > 20) {
    events.shift();
  }
}

export function getBacklogStatus() {
  return {
    queueLength: queue.length,
    running,
    events: [...events].reverse(),
  };
}

export function enqueueBacklogCrawl(seriesId: string) {
  if (queued.has(seriesId) || !canRetry(`backlog:${seriesId}`)) {
    return;
  }
  queued.add(seriesId);
  queue.push(seriesId);

  if (!running) {
    running = true;
    active = processQueue()
      .catch((error) => console.error(`Backlog queue crashed: ${error}`))
      .finally(() => {
        running = false;
        active = null;
      });
  }
}

async function processQueue() {
  while (queue.length > 0) {
    const seriesId = queue.shift()!;
    try {
      await crawlSeries(seriesId);
    } catch (error) {
      deferRetry(`backlog:${seriesId}`);
      console.error(`Backlog crawl failed for ${seriesId}: ${error}`);
    } finally {
      queued.delete(seriesId);
    }
  }
}

async function crawlSeries(seriesId: string) {
  const series = storage.readSeries({ id: seriesId });
  if (!series || series.backlogComplete || !canRetry(`backlog:${seriesId}`)) {
    return;
  }

  console.log(`Backlog crawl for ${seriesId} starting (cursor: ${series.backlogCursor ?? "start"})`);
  recordEvent(`${seriesId}: crawl startet (${series.backlogCursor ? "gjenopptatt" : "fra start"})`);
  let cursor = series.backlogCursor ?? null;

  for (let pageCount = 0; pageCount < MAX_PAGES_PER_RUN; pageCount++) {
    const skipEpisodeIds = storage.readEpisodeIds(seriesId);
    for (const blocked of storage.readBlockedEpisodeIds(seriesId)) {
      skipEpisodeIds.add(blocked);
    }
    const page = await nrkRadio.getEpisodePage(
      seriesId,
      cursor,
      skipEpisodeIds,
      (episodeId) => storage.recordEpisodeFailure(seriesId, episodeId),
      series.catalogKind,
    );
    if (!page) {
      // NRK hiccup: keep the cursor so a later run resumes from here
      console.error(`Backlog crawl for ${seriesId} paused at cursor ${cursor}`);
      recordEvent(`${seriesId}: pauset – NRK utilgjengelig`);
      deferRetry(`backlog:${seriesId}`);
      return;
    }

    if (!storage.hasSeries(seriesId)) return;
    if (page.episodes.length > 0) {
      if (!storage.addEpisodes(seriesId, page.episodes.map(nrkRadio.parseEpisode))) {
        // failed write: don't advance past episodes we didn't persist
        console.error(`Backlog crawl for ${seriesId} paused: episode write failed`);
        recordEvent(`${seriesId}: pauset – databaselagring feilet`);
        deferRetry(`backlog:${seriesId}`);
        return;
      }
    }

    if (page.transientFailures > 0) {
      // NRK hiccups on some manifests: retry this page later instead of
      // advancing past (and losing) those episodes forever
      console.error(
        `Backlog crawl for ${seriesId} paused: ${page.transientFailures} transient manifest failures`,
      );
      recordEvent(`${seriesId}: pauset – ${page.transientFailures} forbigående manifest-feil`);
      deferRetry(`backlog:${seriesId}`);
      return;
    }

    const completedCursor = cursor;
    cursor = page.nextHref;
    // Revisit the last completed page after a pause/restart for overlap.
    storage.setBacklogState(seriesId, cursor === null ? null : completedCursor, cursor === null);

    if (cursor === null) {
      clearRetry(`backlog:${seriesId}`);
      console.log(`Backlog crawl for ${seriesId} complete`);
      recordEvent(`${seriesId}: arkiv komplett`);
      return;
    }

    await new Promise((resolve) => setTimeout(resolve, PAGE_DELAY_MS));
  }

  deferRetry(`backlog:${seriesId}`);
  console.log(`Backlog crawl for ${seriesId} hit the per-run page limit, will resume later`);
}

export const forTestingOnly = {
  waitForIdle: async () => {
    await active;
  },
};
