# NRK URL search

Status: proposal; implementation pending.

Let users paste an NRK podcast, season or episode link into the existing search field and subscribe through the current results and app chooser.

## Behaviour

- Podcast links resolve to the corresponding podcast.
- Season links resolve directly to that season's feed.
- Episode links resolve to their containing podcast or season, clearly identifying what the subscription includes.
- Where applicable, results offer the whole podcast or a specific season.
- Name searches continue working as before. Unsupported or unresolvable links show a helpful message.

Reuse the current design and subscription controls. Preserve existing feed URLs and support both JavaScript-enhanced search and normal form submission. Do not introduce podcast detail pages, a separate URL flow or additional podcast apps.

## Acceptance checks

- Cover podcast, season and episode URLs, including malformed and unsupported links.
- Verify correct podcast/season feed selection.
- Confirm name search and no-JavaScript search remain functional.
- Keep URL resolution within existing NRK caching, concurrency and shared rate limits.
- Restrict resolution to supported NRK hosts and paths; never fetch arbitrary pasted URLs.
