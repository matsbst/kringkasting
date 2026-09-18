/**
 * Feed identity helpers, dependency-free so the client bundle can share
 * them with the server.
 *
 * A feed is either a whole series/podcast (id = NRK's seriesId) or one
 * customSeason of an umbrella podcast (id = "{seriesId}/sesong/{seasonId}",
 * mirroring NRK's own URL structure at radio.nrk.no/podkast/...). The
 * composite id doubles as the storage key and the feed URL path.
 */

/**
 * NRK series/season/episode ids are short slugs. Rejecting anything else
 * keeps user-controlled route params from steering requests to other
 * paths on NRK's API (or growing the database keyspace with garbage).
 */
const RESOURCE_ID_PATTERN = /^[a-zA-Z0-9_-]{1,100}$/;

export function isValidResourceId(id: string): boolean {
  return RESOURCE_ID_PATTERN.test(id);
}

const SEASON_SEPARATOR = "/sesong/";

export function seasonFeedId(seriesId: string, seasonId: string): string {
  return `${seriesId}${SEASON_SEPARATOR}${seasonId}`;
}

export type FeedRef = {
  /** the NRK seriesId (parent podcast for season feeds) */
  seriesId: string;
  /** set only for customSeason feeds */
  seasonId: string | null;
};

export function parseFeedId(feedId: string): FeedRef {
  const separatorIndex = feedId.indexOf(SEASON_SEPARATOR);
  if (separatorIndex === -1) {
    return { seriesId: feedId, seasonId: null };
  }
  return {
    seriesId: feedId.slice(0, separatorIndex),
    seasonId: feedId.slice(separatorIndex + SEASON_SEPARATOR.length),
  };
}

/** a plain series id, or a well-formed "{seriesId}/sesong/{seasonId}" */
export function isValidFeedId(feedId: string): boolean {
  const { seriesId, seasonId } = parseFeedId(feedId);
  return isValidResourceId(seriesId) && (seasonId === null || isValidResourceId(seasonId));
}
