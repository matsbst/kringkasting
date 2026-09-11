import { STATUS_CODE } from "@std/http/status";
import { get } from "../http.ts";
import { Series } from "../storage.ts";
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
export type NrkOriginalEpisode = PodcastEpisodesSingle & { url: string };
export type NrkPodcastEpisode = catalogComponents["schemas"]["PodcastEpisodeHalResource"];
export type NrkSearchResultList = searchComponents["schemas"]["seriesResult"]["results"];
export type SearchResult = ArrayElement<NrkSearchResultList> & {
  description?: string;
};
export type SeriesData =
  & { episodes: NrkOriginalEpisode[] }
  & (
    | RadioSeries["series"]
    | Podcast["series"]
  );

const nrkAPI = `https://psapi.nrk.no`;

/**
 * Upper bound on concurrent requests against NRK's API when resolving
 * playback manifests, so a large series doesn't fire hundreds of
 * requests at once.
 */
const NRK_FETCH_CONCURRENCY = 6;

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

function parseSeries(nrkSeriesData: SeriesData): Series {
  const imageUrl = nrkSeriesData.squareImage?.at(-1)?.url ?? "";
  return {
    id: nrkSeriesData.id,
    title: nrkSeriesData.titles.title,
    subtitle: nrkSeriesData.titles.subtitle ?? null,
    link: `https://radio.nrk.no/podkast/${nrkSeriesData.id}`,
    imageUrl: imageUrl,
    lastFetchedAt: new Date(),
    episodes: nrkSeriesData.episodes.map((episode) => {
      return {
        id: episode.id,
        title: episode.titles.title,
        subtitle: episode.titles.subtitle ?? null,
        url: episode.url,
        shareLink: episode._links.share?.href ?? "",
        date: new Date(episode.date),
        durationInSeconds: episode.durationInSeconds,
      };
    }),
  };
}

async function search(query: string): Promise<NrkSearchResultList | null> {
  const trimmedQuery = query.trim();
  if (trimmedQuery === "") {
    console.error("Empty search query.");
    return null;
  }
  const { status, body } = await get<
    searchComponents["schemas"]["searchresult"]
  >(`${nrkAPI}/radio/search/search?q=${encodeURIComponent(trimmedQuery)}`);
  if (status === STATUS_CODE.OK && body) {
    return body.results.series?.results ?? null;
  }

  console.error(`Something went wrong with ${trimmedQuery} - got status ${status}`);
  return null;
}

/**
 * Resolve download links for the series' episodes.
 *
 * Episodes whose id is in `knownEpisodeIds` are skipped, so refreshing an
 * already-cached series only costs manifest lookups for NEW episodes.
 * Episodes that turn out to be non-playable (geo-blocked, expired rights,
 * removed) are dropped instead of failing the whole series.
 */
async function extractEpisodes(
  serieResponse: RadioSeries,
  episodeResponse?: PodcastEpisodes,
  knownEpisodeIds?: Set<string>,
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

  const newCandidates = knownEpisodeIds ? candidates.filter((episode) => !knownEpisodeIds.has(episode.id)) : candidates;

  const episodes = await mapConcurrent(
    newCandidates,
    NRK_FETCH_CONCURRENCY,
    (episode) => getEpisodeWithDownloadLink(episode, serieResponse.type),
  );

  return episodes.filter((episode): episode is NrkOriginalEpisode => episode !== null);
}

async function getSeriesData(seriesId: string, knownEpisodeIds?: Set<string>): Promise<SeriesData | null> {
  let [
    { status: episodeStatus, body: episodeResponse },
    { status: seriesStatus, body: serieResponse },
  ] = await Promise.all([
    get<PodcastEpisodes>(
      `${nrkAPI}/radio/catalog/podcast/${seriesId}/episodes`,
    ),
    get<Podcast>(`${nrkAPI}/radio/catalog/podcast/${seriesId}`),
  ]);

  if (episodeStatus !== STATUS_CODE.OK || seriesStatus !== STATUS_CODE.OK) {
    [
      { status: episodeStatus, body: episodeResponse },
      { status: seriesStatus, body: serieResponse },
    ] = await Promise.all([
      get<RadioSeriesEpisode>(
        `${nrkAPI}/radio/catalog/series/${seriesId}/episodes`,
      ),
      get<RadioSeries>(`${nrkAPI}/radio/catalog/series/${seriesId}`),
    ]);
  }

  if (
    episodeStatus === STATUS_CODE.OK &&
    seriesStatus === STATUS_CODE.OK &&
    serieResponse?.series &&
    episodeResponse?._embedded.episodes?.length
  ) {
    const episodes = await extractEpisodes(serieResponse, episodeResponse, knownEpisodeIds);
    const seriesData = {
      ...serieResponse.series,
      episodes,
    };
    return seriesData;
  }
  console.error(
    `Error getting episodes for ${seriesId}: EpisodeStatus: ${episodeStatus}. SerieStatus: ${seriesStatus}`,
  );
  return null;
}

/**
 * Fetch a series with episode download links.
 *
 * When `knownEpisodeIds` is given, the returned series only contains
 * episodes NOT in that set (an incremental update); the caller is
 * expected to merge with its existing episodes.
 */
async function getSeries(seriesId: string, knownEpisodeIds?: Set<string>): Promise<Series | null> {
  const seriesData = await getSeriesData(seriesId, knownEpisodeIds);
  if (!seriesData) {
    return null;
  }
  const parsedSeries = parseSeries(seriesData);
  return parsedSeries;
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

async function getEpisodeWithDownloadLink(
  episode: PodcastEpisodesSingle,
  type: catalogComponents["schemas"]["Type"],
): Promise<NrkOriginalEpisode | null> {
  const endpoint = type === "series" ? "program" : "podcast";
  const { status, body } = await get<Manifest>(
    `${nrkAPI}/playback/manifest/${endpoint}/${episode.episodeId}`,
  );

  // non-OK statuses and playable=null (non-playable manifests) both mean
  // the episode has no usable download link right now; skip it
  const url = body?.playable?.assets?.at(0)?.url;
  if (status !== STATUS_CODE.OK || !url) {
    console.error(
      `No playable manifest for episode ${episode.episodeId} (status ${status}), skipping`,
    );
    return null;
  }

  return { ...episode, url };
}

export const nrkRadio = {
  search,
  getSeries,
  getEpisode,
  parseSeries,
};

export const forTestingOnly = {
  getSeriesData,
  mapConcurrent,
};
