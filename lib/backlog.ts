import { nrkRadio } from "./nrk/nrk.ts";
import { storage } from "./storage.ts";

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

export function enqueueBacklogCrawl(seriesId: string) {
  if (queued.has(seriesId)) {
    return;
  }
  queued.add(seriesId);
  queue.push(seriesId);

  if (!running) {
    running = true;
    processQueue()
      .catch((error) => console.error(`Backlog queue crashed: ${error}`))
      .finally(() => {
        running = false;
      });
  }
}

async function processQueue() {
  while (queue.length > 0) {
    const seriesId = queue.shift()!;
    try {
      await crawlSeries(seriesId);
    } catch (error) {
      console.error(`Backlog crawl failed for ${seriesId}: ${error}`);
    } finally {
      queued.delete(seriesId);
    }
  }
}

async function crawlSeries(seriesId: string) {
  const series = storage.readSeries({ id: seriesId });
  if (!series || series.backlogComplete) {
    return;
  }

  console.log(`Backlog crawl for ${seriesId} starting (cursor: ${series.backlogCursor ?? "start"})`);
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
    );
    if (!page) {
      // NRK hiccup: keep the cursor so a later run resumes from here
      console.error(`Backlog crawl for ${seriesId} paused at cursor ${cursor}`);
      return;
    }

    if (page.episodes.length > 0) {
      storage.addEpisodes(seriesId, page.episodes.map(nrkRadio.parseEpisode));
    }

    cursor = page.nextHref;
    storage.setBacklogState(seriesId, cursor, cursor === null);

    if (cursor === null) {
      console.log(`Backlog crawl for ${seriesId} complete`);
      return;
    }

    await new Promise((resolve) => setTimeout(resolve, PAGE_DELAY_MS));
  }

  console.log(`Backlog crawl for ${seriesId} hit the per-run page limit, will resume later`);
}
