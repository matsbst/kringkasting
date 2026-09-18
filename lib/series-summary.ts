import { seasonFeedId } from "./feed-id.ts";
import type { SearchResult } from "./nrk/nrk.ts";

/**
 * The slim, serializable shape the UI needs for a search hit — sent to
 * the InstantSearch island and over /api/search instead of NRK's full
 * search payload.
 */
export type SeriesSummary = {
  seriesId: string;
  /** set when the hit is a customSeason — a standalone show published as a season of an umbrella podcast */
  seasonId?: string;
  title: string;
  description: string | null;
  imageUrl: string | null;
  /** NRK offers this show only as a stream (HLS); needs an HLS-capable app */
  streamOnly?: boolean;
};

/** the feed id — also the path under /api/feeds/ and NRK's path under /podkast/ */
export function feedIdOf(summary: Pick<SeriesSummary, "seriesId" | "seasonId">): string {
  return summary.seasonId ? seasonFeedId(summary.seriesId, summary.seasonId) : summary.seriesId;
}

/** pick the image variant closest to the wanted rendered size */
function pickImage(images: SearchResult["images"], wantedWidth = 400) {
  if (!images || images.length === 0) {
    return null;
  }
  return images.reduce((best, candidate) =>
    Math.abs((candidate.width ?? 0) - wantedWidth) < Math.abs((best.width ?? 0) - wantedWidth) ? candidate : best
  );
}

export function toSeriesSummary(result: SearchResult): SeriesSummary {
  // prefer square (1:1) artwork over the default 16:9 images
  const image = pickImage(result.images_1_1 ?? result.images);
  return {
    seriesId: result.seriesId,
    // NRK publishes standalone shows as customSeasons of umbrella
    // podcasts; those get their own (season) feed, mirroring NRK
    ...(result.type === "customSeason" && result.seasonId ? { seasonId: result.seasonId } : {}),
    title: result.title,
    description: result.description ?? null,
    imageUrl: image?.uri ?? null,
  };
}

/**
 * Map NRK results to summaries, flagging the ones we've already crawled
 * and found to be stream-only. The lookup is supplied by the caller
 * (server-only) so this module stays free of storage/client-bundle deps.
 */
export function toSummaries(
  results: SearchResult[],
  streamOnlyLookup: (feedIds: string[]) => Set<string>,
): SeriesSummary[] {
  const summaries = results.map(toSeriesSummary);
  const streamOnlyIds = streamOnlyLookup(summaries.map(feedIdOf));
  return summaries.map((summary) => streamOnlyIds.has(feedIdOf(summary)) ? { ...summary, streamOnly: true } : summary);
}
