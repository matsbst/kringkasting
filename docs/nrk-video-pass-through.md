# Pass NRK video through in podcast feeds

Status: implemented (PR #13); player playback tests pending.

## Purpose

Expose video that NRK provides for podcast episodes through standard RSS fields, so capable podcast players can discover and play it. Kringkasting remains a bridge from NRK to the listener's chosen app and makes no editorial selections.

## Behaviour

- Preserve existing feed URLs, episode GUIDs, ordering, inclusion rules and audio enclosure URLs.
- For episodes with confirmed available NRK video, add podcast:alternateEnclosure containing a podcast:source with NRK's own video playlist URL.
- Preserve NRK's adaptive HLS master playlist rather than choosing a resolution. The podcast player decides whether and how to play video.
- Apply the same metadata-driven rules to podcast and season feeds, without show allowlists or player-specific filtering.
- Do not add duplicate episode entries or substitute video for the audio enclosure.
- Video lookup failures must not prevent audio episodes from appearing or being served.

## Metadata and persistence

Capture NRK's clipId when present in catalog responses. Resolve its playback metadata through the existing upstream client and confirm playable video before advertising it. Do not assume every clipId identifies a full-length video counterpart; use representative fixtures to verify the mapping and avoid attaching unrelated promotional clips.

Store the video metadata with additive SQLite changes, preserving existing archives and compatibility with the previous image. Resolve and refresh metadata within existing caching, request coalescing, concurrency and retry limits. Do not resolve every episode's video on every feed poll.

Use NRK URLs directly. Validate playlist URL longevity and whether normal metadata refreshes keep URLs current; do not assume permanence or introduce a redirect endpoint as a workaround. Cover transient failures, confirmed unavailability and stale metadata explicitly. Recover video metadata for previously stored episodes gradually within existing upstream limits, without resetting archives or launching an unrestricted recrawl.

## Scope boundaries

- No Kringkasting video/media URLs or redirect endpoints.
- No media proxying, playlist rewriting, downloading, hosting or transcoding.
- No separate video feeds, editorial curation or changes to episode identity.
- No website, search, subscription chooser or podcast app list changes.
- No player-specific feed variants. Players that understand the extension can use it; other players retain the audio enclosure.

## Acceptance criteria

- Audio-only episode output remains unchanged.
- Video-capable episodes retain their existing audio enclosure and GUID, with one standards-compliant alternate enclosure pointing directly to NRK.
- The source is NRK's adaptive video master playlist, with honest MIME metadata and no invented file size, bitrate or resolution.
- Podcast and season feeds resolve the correct video for the same underlying episode.
- Missing, non-video, unavailable and transiently failing clip manifests do not break the audio feed.
- Adding or updating video metadata invalidates rendered feed caches/validators as appropriate.
- Persisted video metadata survives restarts; database upgrade and rollback preserve archive state.
- Concurrent feed requests do not multiply video lookups; feed polling uses cached metadata and existing upstream limits.
- Representative current and older NRK playlists are checked for URL validity, redirects, codecs and audio/video correspondence without copying the media.
- Test playback using the same feed in Pocket Casts and Podcast Addict, including seeking and audio fallback. Verify an audio-only player still uses the unchanged audio enclosure.
- No website changes or Kringkasting media endpoints are introduced.

## Implementation

- **Clip references** (`clipId`) are captured from catalog listings the app fetches anyway: initial fetches, refreshes and archive crawl pages report them for every listed episode, including ones already stored. No extra requests.
- **Video lookup** (`internal/video`): a background worker, queued after a feed is fetched or refreshed (never on plain polls). One feed at a time, at most 20 due episodes per round, two lookups at a time, through the shared NRK client (global cap, retry deadlines). Concurrent lookups of the same clip share one request; a result already found for the same clip in another feed (a podcast and its season feed) is reused.
- **Matching**: `GET /playback/manifest/clip/{clipId}` must report `sourceMedium: video`, `playability: playable` and an unencrypted HLS asset, and the clip's duration must match the audio within max(60 s, 5 %). Trailers and promos fail the duration check.
- **Re-checks**: a confirmed video is re-checked after 30 days (which also refreshes the URL); an episode without video is re-checked after 6 hours while under a week old (NRK can publish the video after the audio), otherwise after 30 days. Transient failures use the retry deadlines (`retry:video:{clipId}`) and change nothing.
- **Older episodes**: for shows with at least one video and a complete archive crawl, clip references are recovered one listing page per round (`video-backfill:{feed}` in `app_state`), with no playback lookups.
- **Storage**: additive `episodes` columns `clip_id`, `video_url`, `video_next_check_at`. Episode upserts never touch the video columns; a change of `video_url` bumps the feed's data version, so the rendered feed and its ETag move.
- **Feed**: `<podcast:alternateEnclosure type="application/x-mpegURL" length="0" lang="no" title="Video"><podcast:source uri="…NRK playlist…"/></podcast:alternateEnclosure>` after the audio enclosure. `length="0"` is RSS's "unknown", as for enclosures; no bitrate or height is invented.

Verified (2026-10-15):

- Audio-only output is byte-identical to before (all golden feed tests, whose fixtures have no clip references).
- Live, Sentralbordet: all 10 episodes get video; every item is otherwise unchanged; the XML is well-formed; playlists answer 200 and match the audio length.
- The previous release serves a database written by this version (it ignores the new columns) and keeps the stored video data.
- Tests cover matching, promo/audio-only/encrypted/missing clips, transient failures and recovery, re-checks, coalescing across feeds and requests, persistence across restarts, cache invalidation and the bounded backfill.

Still to do before marking ready: playback in Pocket Casts and Podcast Addict (including seeking and audio fallback), and an audio-only player with the same feed.

## References

- NRK example: https://radio.nrk.no/podkast/sentralbordet
- Podcast namespace: https://github.com/Podcastindex-org/podcast-namespace/blob/main/docs/tags/alternate-enclosure.md
- HLS feed example: https://github.com/Podcast-Standards-Project/hls-video
- Pocket Casts: https://support.pocketcasts.com/knowledge-base/video-podcasts/
- Podcast Addict: https://podcastaddict.com/changelog/2026_10
