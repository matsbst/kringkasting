import { STATUS_CODE } from "@std/http/status";
import { get, head } from "../http.ts";
import { CatalogKind, Episode, Series } from "../storage.ts";
import { components as catalogComponents } from "./nrk-catalog.ts";
import { external as playbackComponents } from "./nrk-playback.ts";
import { components as searchComponents } from "./nrk-search.ts";

type ArrayElement<A> = A extends readonly (infer T)[] ? T : never;
type PodcastEpisodes = catalogComponents["schemas"]["EpisodesHalResource"];
type Podcast = catalogComponents["schemas"]["SeriesHalResource"];
type PodcastEpisodesSingle = catalogComponents["schemas"]["EpisodeHalResource"];
type RadioSeriesEpisode = catalogComponents["schemas"]["EpisodesHalResource"];
type RadioSeries = catalogComponents["schemas"]["SeriesHalResource"];
type SeasonEpisodes = catalogComponents["schemas"]["PodcastSeasonHalResource"];

export type NrkSerie = catalogComponents["schemas"]["SeriesViewModel"];
export type NrkOriginalEpisode = PodcastEpisodesSingle & { url: string; bytes?: number | null };
export type NrkPodcastEpisode = catalogComponents["schemas"]["PodcastEpisodeHalResource"];
export type NrkSearchResultList = searchComponents["schemas"]["seriesResult"]["results"];
export type SearchResult = ArrayElement<NrkSearchResultList> & {
  description?: string;
};
export type SeriesData =
  & { episodes: NrkOriginalEpisode[]; catalogKind: CatalogKind; isUmbrella: boolean }
  & (
    | RadioSeries["series"]
    | Podcast["series"]
  );

export type FetchOptions = {
  /** episodes to skip (already stored, or under failure backoff) */
  skipEpisodeIds?: Set<string>;
  onEpisodeFailure?: OnEpisodeFailure;
  /** stored catalog kind: try that endpoint family first, sparing the fallback */
  catalogKind?: CatalogKind | null;
};

/** endpoint families in the order worth trying */
function catalogOrder(preferred?: CatalogKind | null): CatalogKind[] {
  return preferred === "series" ? ["series", "podcast"] : ["podcast", "series"];
}

const nrkAPI = `https://psapi.nrk.no`;

/**
 * Upper bound on concurrent requests against NRK's API when resolving
 * playback manifests, so a large series doesn't fire hundreds of
 * requests at once.
 */
const NRK_FETCH_CONCURRENCY = 6;

/** the background backlog crawl is gentler than interactive requests */
const BACKLOG_FETCH_CONCURRENCY = 3;

/** map over items with at most `limit` promises in flight */
async function mapConcurrent<T, R>(items: T[], limit: number, fn: (item: T) => Promise<R>): Promise<R[]> {
  const results: R[] = new Array(items.length);
  let nextIndex = 0;
  const workers = new Array(Math.min(limit, items.length)).fill(null).map(async () => {
    while (nextIndex < items.length) {
      const index = nextIndex++;
      results[index] = await fn(items[index]);
    }
  });
  await Promise.all(workers);
  return results;
}

function parseEpisode(episode: NrkOriginalEpisode): Episode {
  return {
    id: episode.id,
    title: episode.titles.title,
    subtitle: episode.titles.subtitle ?? null,
    url: episode.url,
    shareLink: episode._links.share?.href ?? "",
    date: new Date(episode.date),
    durationInSeconds: episode.durationInSeconds,
    bytes: episode.bytes ?? null,
  };
}

function parseSeries(nrkSeriesData: SeriesData): Series {
  const imageUrl = nrkSeriesData.squareImage?.at(-1)?.url ?? "";
  return {
    id: nrkSeriesData.id,
    title: nrkSeriesData.titles.title,
    subtitle: nrkSeriesData.titles.subtitle ?? null,
    link: `https://radio.nrk.no/podkast/${nrkSeriesData.id}`,
    imageUrl: imageUrl,
    lastFetchedAt: new Date(),
    catalogKind: nrkSeriesData.catalogKind,
    isUmbrella: nrkSeriesData.isUmbrella,
    episodes: nrkSeriesData.episodes.map(parseEpisode),
  };
}

/**
 * Micro-cache for search results: the homepage suggestion chips make
 * many visitors run identical searches, and repeated queries are common.
 * Only successful results are cached; errors always retry.
 */
const SEARCH_CACHE_TTL_MS = 5 * 60 * 1000;
const SEARCH_CACHE_MAX_ENTRIES = 200;
const searchCache = new Map<string, { expires: number; result: NrkSearchResultList }>();

async function search(query: string): Promise<NrkSearchResultList | null> {
  const trimmedQuery = query.trim();
  if (trimmedQuery === "") {
    console.error("Empty search query.");
    return null;
  }

  const cacheKey = trimmedQuery.toLowerCase();
  const cached = searchCache.get(cacheKey);
  if (cached && cached.expires > Date.now()) {
    return cached.result;
  }

  const { status, body } = await get<
    searchComponents["schemas"]["searchresult"]
  >(`${nrkAPI}/radio/search/search?q=${encodeURIComponent(trimmedQuery)}`);
  if (status === STATUS_CODE.OK && body) {
    const result = body.results.series?.results ?? null;
    if (result) {
      if (searchCache.size >= SEARCH_CACHE_MAX_ENTRIES) {
        const now = Date.now();
        for (const [key, entry] of searchCache) {
          if (entry.expires <= now) {
            searchCache.delete(key);
          }
        }
        // still full of fresh entries: drop the oldest
        if (searchCache.size >= SEARCH_CACHE_MAX_ENTRIES) {
          const oldest = searchCache.keys().next().value;
          if (oldest !== undefined) {
            searchCache.delete(oldest);
          }
        }
      }
      searchCache.set(cacheKey, { expires: Date.now() + SEARCH_CACHE_TTL_MS, result });
    }
    return result;
  }

  console.error(`Something went wrong with ${trimmedQuery} - got status ${status}`);
  return null;
}

/** notified when an episode is definitively unavailable (for retry backoff) */
export type OnEpisodeFailure = (episodeId: string) => void;

/**
 * Resolve download links for the series' episodes.
 *
 * Episodes whose id is in `skipEpisodeIds` are skipped, so refreshing an
 * already-cached series only costs manifest lookups for NEW episodes
 * (and episodes under failure backoff are not re-checked).
 * Episodes that turn out to be non-playable (geo-blocked, expired rights,
 * removed) are dropped instead of failing the whole series, and reported
 * via `onEpisodeFailure`.
 */
async function extractEpisodes(
  serieResponse: RadioSeries,
  episodeResponse?: PodcastEpisodes,
  skipEpisodeIds?: Set<string>,
  onEpisodeFailure?: OnEpisodeFailure,
): Promise<NrkOriginalEpisode[]> {
  let candidates: PodcastEpisodesSingle[];

  if (serieResponse.seriesType === "umbrella") {
    const seasons = await mapConcurrent(
      serieResponse._links.seasons,
      NRK_FETCH_CONCURRENCY,
      async (season) => {
        const response = await get<SeasonEpisodes>(`${nrkAPI}${season.href}`);
        return response.body;
      },
    );
    candidates = seasons.flatMap((season) => season?._embedded.episodes?._embedded.episodes ?? []);
  } else {
    candidates = episodeResponse?._embedded.episodes ?? [];
  }

  const newCandidates = skipEpisodeIds ? candidates.filter((episode) => !skipEpisodeIds.has(episode.id)) : candidates;

  const results = await mapConcurrent(
    newCandidates,
    NRK_FETCH_CONCURRENCY,
    (episode) => getEpisodeWithDownloadLink(episode, serieResponse.type),
  );

  return collectEpisodes(newCandidates, results, onEpisodeFailure);
}

/** separate playable episodes from definitive failures, reporting the latter */
function collectEpisodes(
  candidates: PodcastEpisodesSingle[],
  results: (NrkOriginalEpisode | "gone" | null)[],
  onEpisodeFailure?: OnEpisodeFailure,
): NrkOriginalEpisode[] {
  const episodes: NrkOriginalEpisode[] = [];
  results.forEach((result, index) => {
    if (result === "gone") {
      onEpisodeFailure?.(candidates[index].id);
    } else if (result !== null) {
      episodes.push(result);
    }
  });
  return episodes;
}

async function getSeriesData(seriesId: string, options: FetchOptions = {}): Promise<SeriesData | null> {
  let episodeStatus = 0;
  let seriesStatus = 0;
  let episodeResponse: PodcastEpisodes | null = null;
  let serieResponse: Podcast | RadioSeries | null = null;
  let usedKind: CatalogKind = "podcast";

  // stored catalog kind goes first, so the wrong-family requests are
  // only spent on brand-new series (or ones NRK has recatalogued)
  for (const kind of catalogOrder(options.catalogKind)) {
    [
      { status: episodeStatus, body: episodeResponse },
      { status: seriesStatus, body: serieResponse },
    ] = await Promise.all([
      get<PodcastEpisodes>(`${nrkAPI}/radio/catalog/${kind}/${seriesId}/episodes`),
      get<Podcast>(`${nrkAPI}/radio/catalog/${kind}/${seriesId}`),
    ]);
    usedKind = kind;
    if (episodeStatus === STATUS_CODE.OK && seriesStatus === STATUS_CODE.OK) {
      break;
    }
  }

  if (
    episodeStatus === STATUS_CODE.OK &&
    seriesStatus === STATUS_CODE.OK &&
    serieResponse?.series &&
    episodeResponse?._embedded.episodes?.length
  ) {
    const episodes = await extractEpisodes(
      serieResponse,
      episodeResponse,
      options.skipEpisodeIds,
      options.onEpisodeFailure,
    );
    return {
      ...serieResponse.series,
      episodes,
      catalogKind: usedKind,
      isUmbrella: serieResponse.seriesType === "umbrella",
    };
  }
  console.error(
    `Error getting episodes for ${seriesId}: EpisodeStatus: ${episodeStatus}. SerieStatus: ${seriesStatus}`,
  );
  return null;
}

/**
 * Fetch a series with episode download links.
 *
 * When `options.skipEpisodeIds` is given, the returned series only
 * contains episodes NOT in that set (an incremental update); the caller
 * is expected to merge with its existing episodes.
 */
async function getSeries(seriesId: string, options: FetchOptions = {}): Promise<Series | null> {
  const seriesData = await getSeriesData(seriesId, options);
  if (!seriesData) {
    return null;
  }
  return parseSeries(seriesData);
}

/**
 * Cheap refresh for non-umbrella series with fresh metadata: fetches the
 * episode listing only (one request instead of two) and resolves
 * manifests for episodes not in the skip set.
 */
async function getNewEpisodes(
  seriesId: string,
  catalogKind: CatalogKind,
  options: FetchOptions = {},
): Promise<NrkOriginalEpisode[] | null> {
  const { status, body } = await get<PodcastEpisodes>(
    `${nrkAPI}/radio/catalog/${catalogKind}/${seriesId}/episodes`,
  );
  if (status !== STATUS_CODE.OK || !body) {
    return null;
  }

  const candidates = body._embedded.episodes ?? [];
  const newCandidates = options.skipEpisodeIds
    ? candidates.filter((episode) => !options.skipEpisodeIds!.has(episode.id))
    : candidates;

  const results = await mapConcurrent(
    newCandidates,
    NRK_FETCH_CONCURRENCY,
    (episode) => getEpisodeWithDownloadLink(episode, catalogKind),
  );
  return collectEpisodes(newCandidates, results, options.onEpisodeFailure);
}

async function getEpisode(
  seriesId: string,
  episodeId: string,
): Promise<NrkPodcastEpisode | null> {
  // a series can be catalogued as a podcast or a radio series; try both
  for (const catalog of ["podcast", "series"] as const) {
    const url = `${nrkAPI}/radio/catalog/${catalog}/${seriesId}/episodes/${episodeId}`;
    const { status, body: episode } = await get<NrkPodcastEpisode>(url);
    if (status === STATUS_CODE.OK && episode) {
      return episode;
    }
  }
  console.error(`Error getting episode ${episodeId} for series ${seriesId}`);
  return null;
}

type Manifest = playbackComponents["schemas/playback-channel.json"]["components"]["schemas"]["PlayableManifest"];

/**
 * Returns the episode with its download link, "gone" when NRK
 * definitively has no playable manifest (404, or 200 with playable=null:
 * geo-blocked/expired/removed — worth backing off from), or null on
 * transient errors (timeouts, 5xx — retry next refresh).
 */
async function getEpisodeWithDownloadLink(
  episode: PodcastEpisodesSingle,
  type: catalogComponents["schemas"]["Type"],
): Promise<NrkOriginalEpisode | "gone" | null> {
  const endpoint = type === "series" ? "program" : "podcast";
  const { status, body } = await get<Manifest>(
    `${nrkAPI}/playback/manifest/${endpoint}/${episode.episodeId}`,
  );

  const url = body?.playable?.assets?.at(0)?.url;
  if (status === STATUS_CODE.OK && url) {
    // RSS enclosures want the file size in bytes
    const { contentLength } = await head(url);
    return { ...episode, url, bytes: contentLength };
  }

  console.error(`No playable manifest for episode ${episode.episodeId} (status ${status})`);
  if (status === STATUS_CODE.NotFound || status === STATUS_CODE.OK) {
    return "gone";
  }
  return null;
}

export type EpisodePage = {
  episodes: NrkOriginalEpisode[];
  /** NRK API href of the next page, or null when this was the last page */
  nextHref: string | null;
};

/**
 * Fetch one page of a series' episode archive, resolving download links
 * only for episodes not in `knownEpisodeIds`. Pass cursorHref=null to
 * start from the first page (both podcast and radio-series catalogs are
 * tried); afterwards pass the returned nextHref.
 *
 * Returns null when the page could not be fetched — the caller can retry
 * later from the same cursor.
 */
async function getEpisodePage(
  seriesId: string,
  cursorHref: string | null,
  skipEpisodeIds: Set<string>,
  onEpisodeFailure?: OnEpisodeFailure,
  catalogKind?: CatalogKind | null,
): Promise<EpisodePage | null> {
  let href = cursorHref;
  let body: PodcastEpisodes | null = null;

  if (href) {
    ({ body } = await get<PodcastEpisodes>(`${nrkAPI}${href.startsWith("/") ? "" : "/"}${href}`));
  } else {
    for (const catalog of catalogOrder(catalogKind)) {
      href = `/radio/catalog/${catalog}/${seriesId}/episodes?page=1&pageSize=50`;
      ({ body } = await get<PodcastEpisodes>(`${nrkAPI}${href}`));
      if (body) {
        break;
      }
    }
  }

  if (!body || !href) {
    return null;
  }

  // the catalog kind in the href decides which playback endpoint to use
  const type = href.includes("/catalog/series/") ? "series" : "podcast";

  const candidates = body._embedded.episodes ?? [];
  const newCandidates = candidates.filter((episode) => !skipEpisodeIds.has(episode.id));

  const results = await mapConcurrent(
    newCandidates,
    BACKLOG_FETCH_CONCURRENCY,
    (episode) => getEpisodeWithDownloadLink(episode, type),
  );

  return {
    episodes: collectEpisodes(newCandidates, results, onEpisodeFailure),
    nextHref: body._links.next?.href ?? null,
  };
}

export const nrkRadio = {
  search,
  getSeries,
  getNewEpisodes,
  getEpisode,
  getEpisodePage,
  parseSeries,
  parseEpisode,
};

export const forTestingOnly = {
  getSeriesData,
  mapConcurrent,
};
