# Pass NRK video through in podcast feeds

Status: proposal; implementation pending.

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

## References

- NRK example: https://radio.nrk.no/podkast/sentralbordet
- Podcast namespace: https://github.com/Podcastindex-org/podcast-namespace/blob/main/docs/tags/alternate-enclosure.md
- HLS feed example: https://github.com/Podcast-Standards-Project/hls-video
- Pocket Casts: https://support.pocketcasts.com/knowledge-base/video-podcasts/
- Podcast Addict: https://podcastaddict.com/changelog/2026_10
