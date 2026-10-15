# NRK URL search

Status: implemented (PR #12).

Let users paste an NRK podcast, season or episode link into the existing search field and subscribe through the current results and app chooser.

## Behaviour

- Podcast links resolve to the corresponding podcast.
- Season links resolve directly to that season's feed.
- Episode links resolve to their containing podcast or season, clearly identifying what the subscription includes.
- Where applicable, results offer the whole podcast or a specific season.
- Name searches continue working as before. Unsupported or unresolvable links show a helpful message.

Reuse the current design and subscription controls. Preserve existing feed URLs and support both JavaScript-enhanced search and normal form submission. Do not introduce podcast detail pages, a separate URL flow or additional podcast apps.

## Implementation

Supported links, on `radio.nrk.no` only (with or without `https://`; query strings and fragments are ignored):

| Link | Results |
| --- | --- |
| `/podkast/{id}`, `/serie/{id}` | the podcast or radio series |
| `/podkast/{id}/sesong/{season}`, `/serie/{id}/sesong/{season}` | the season's feed, then the whole podcast or series |
| `/podkast/{id}/{episode}` (NRK's share-link form) | for an umbrella podcast: the episode's season, then the whole podcast; otherwise the whole podcast |
| `/podkast/{id}/sesong/{season}/{episode}` | the season, then the whole podcast |

Umbrella podcasts publish standalone shows as seasons (Musikkhistorier, Hele historien), so an episode link offers that show. Ordinary podcasts also have numbered seasons (Supertrøtt's season `1`); there an episode link offers the whole podcast. When both a season and the whole podcast are offered, each card is labelled ("Sesong i «…»", "Hele podkasten" / "Hele serien"); an episode link adds a note that subscribing gives all episodes.

Other links (other NRK sites, other hosts, unknown paths, ids that aren't NRK-style slugs) show "Lenken støttes ikke"; a supported link to a show NRK doesn't know shows "Fant ikke podkasten i lenken".

- `internal/nrklink` parses the text; the pasted URL is never fetched.
- `nrk.Client.Resolve` calls only NRK's catalog API with the validated ids, through the shared concurrency cap and retry deadlines, and caches results for 5 minutes.
- The page (`/?query=`), `/api/search` and `/api/search/fragment` share one code path and the search rate limit. Links may be up to 500 characters (names: 2–100).

## Acceptance checks

- Cover podcast, season and episode URLs, including malformed and unsupported links. — `internal/nrklink` (parsing), `internal/e2e/link_test.go` (resolution and pages).
- Verify correct podcast/season feed selection. — `TestLinkSearch` checks the feed URLs offered for each link type; `go test -tags live ./internal/nrk/` checks real NRK links.
- Confirm name search and no-JavaScript search remain functional. — `TestLinkSearchEntryPoints` and the existing golden tests.
- Keep URL resolution within existing NRK caching, concurrency and shared rate limits. — `TestLinkSearch` (cache), `TestLinkSearchEntryPoints` (rate limit).
- Restrict resolution to supported NRK hosts and paths; never fetch arbitrary pasted URLs. — `TestLinkMessages` and `TestLinkSearch` assert every request goes to `psapi.nrk.no`.
