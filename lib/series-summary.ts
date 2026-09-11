import type { SearchResult } from "./nrk/nrk.ts";

/**
 * The slim, serializable shape the UI needs for a search hit — sent to
 * the InstantSearch island and over /api/search instead of NRK's full
 * search payload.
 */
export type SeriesSummary = {
  seriesId: string;
  title: string;
  description: string | null;
  imageUrl: string | null;
};

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
    title: result.title,
    description: result.description ?? null,
    imageUrl: image?.uri ?? null,
  };
}
