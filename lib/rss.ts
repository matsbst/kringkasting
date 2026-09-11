import { declaration, serialize, Tag, tag } from "./xml.ts";
import { Episode, Series } from "./storage.ts";

/**
 * @param origin the public origin of this instance, used for
 * absolute URLs pointing back to the app (e.g. chapters).
 */
function assembleFeed(series: Series, origin: string): string {
  // Originally adapted from https://raw.githubusercontent.com/olaven/paperpod/1cde9abd3174b26e126aa74fc5a3b63fd078c0fd/packages/converter/src/rss.ts
  return serialize(
    declaration([
      ["version", "1.0"],
      ["encoding", "UTF-8"],
    ]),
    tag(
      "rss",
      [
        tag("channel", [
          tag("title", series.title),
          tag("link", series.link),
          tag("itunes:author", "NRK"),
          /**
           * serie.category.id does not overlap with Apple's supported categories..
           * These podcast feeds are not going to be indexed in itunes anyways, so
           * a static, valid category is fine. The point is simply to pass third party
           * podcast feed validation.
           */
          tag("itunes:category", "", [["text", "Government"]]),
          tag("itunes:owner", [
            tag("itunes:name", "NRK"),
            tag("itunes:email", "nrkpodcast@nrk.no"),
          ]),
          tag("description", series.subtitle || ""),
          tag("ttl", "60"), //60 minutes
          ...(series.imageUrl
            ? [
              tag("itunes:image", "", [["href", series.imageUrl]]),
              tag("image", [
                tag("url", series.imageUrl),
                tag("title", series.title),
                tag("link", series.link),
              ]),
            ]
            : []),
          ...series.episodes.map((episode) => assembleEpisode(episode, series.id, origin)),
        ]),
      ],
      [
        ["version", "2.0"],
        ["xmlns:itunes", "http://www.itunes.com/dtds/podcast-1.0.dtd"],
        ["xmlns:content", "http://purl.org/rss/1.0/modules/content/"],
        ["xmlns:podcast", "https://podcastindex.org/namespace/1.0"],
      ],
    ),
  );
}

function assembleEpisode(episode: Episode, seriesId: Series["id"], origin: string): Tag {
  const description = episode.subtitle || "";

  return tag("item", [
    tag("title", episode.title),
    tag("link", episode.shareLink),
    tag("description", description),
    tag("itunes:summary", description),
    tag("guid", episode.id, [["isPermaLink", "false"]]),
    tag("pubDate", new Date(episode.date).toUTCString()),
    tag("itunes:duration", episode.durationInSeconds.toString()),
    tag("podcast:chapters", "", [
      ["url", `${origin}/api/feeds/${seriesId}/${episode.id}/chapters`],
      ["type", "application/json+chapters"],
    ]),
    tag("enclosure", "", [
      ["url", episode.url],
      // RSS defines length as the file size in bytes; NRK's API does not
      // expose it, and "0" is the conventional value for "unknown"
      ["length", "0"],
      ["type", "audio/mpeg"],
    ]),
  ]);
}

export const rss = {
  assembleFeed,
};

export const forTestingOnly = {
  assembleEpisode,
};
