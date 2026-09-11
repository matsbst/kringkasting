# Bruk av NRKs API (psapi.nrk.no)

_Dette dokumentet er skrevet for NRK: en fullstendig og ærlig oversikt over hvilke forespørsler Kringkasting gjør mot NRKs åpne API, hvor ofte, og hvilke tiltak som er gjort for å belaste tjenestene minst mulig. Spørsmål eller ønsker om endring? [Opprett en sak her på GitHub](../../issues) — vi retter oss etter dem._

## Hva tjenesten gjør

Kringkasting ([kringkast.ing](https://kringkast.ing)) genererer åpne RSS-strømmer for NRK sine podkaster, slik at lyttere kan abonnere i valgfri podkast-app. Tjenesten er ikke-kommersiell, åpen kildekode (AGPL-3.0) og uten tilknytning til NRK. **Alt lyd- og bildeinnhold leveres direkte fra NRK sine CDN-er** (`podkast.nrk.no`, `gfx.nrk.no`) til lytterens klient — vi proxyer eller mellomlagrer aldri mediefiler.

Det sentrale designprinsippet: **antall lyttere påvirker ikke belastningen på NRK.** Alle abonnent-oppslag besvares fra vår egen database og CDN-kant; NRK ser kun tjenestens egne, planlagte oppdateringer.

## Endepunkter som brukes

| Endepunkt                                               | Når                                | Frekvens                                                       |
| ------------------------------------------------------- | ---------------------------------- | -------------------------------------------------------------- |
| `GET /radio/search/search?q=`                           | Bruker søker på nettsiden          | Mellomlagret 5 min per søkeord                                 |
| `GET /radio/search/categories/podcast`                  | Katalogliste (forslags-chips)      | ~7 sideforespørsler per døgn, totalt                           |
| `GET /radio/catalog/{podcast\|series}/{id}`             | Seriemetadata (tittel/omslag)      | Ved første oppslag, deretter maks ukentlig per serie           |
| `GET /radio/catalog/{podcast\|series}/{id}/episodes`    | Nye episoder                       | Adaptivt per serie: se tabellen under                          |
| `GET …/episodes?page=N&pageSize=50`                     | Engangs arkiv-innhenting per serie | Sekvensielt med 1 s pause mellom sider; gjenopptas ved avbrudd |
| `GET /playback/manifest/{podcast\|program}/{episodeId}` | Nedlastingslenke for episode       | Én gang per ny episode                                         |
| `HEAD` på MP3 hos `podkast.nrk.no`                      | Filstørrelse til RSS-enclosure     | Én gang per ny episode                                         |
| `GET …/episodes/{episodeId}`                            | Kapittel-data                      | Kun når en podkast-app ber om kapitler                         |

## Oppdateringsfrekvens per serie (adaptiv)

Hvor ofte en serie sjekkes for nye episoder styres av hvor aktiv den er:

| Nyeste episode                    | Sjekkes          |
| --------------------------------- | ---------------- |
| < 2 døgn gammel                   | hver time        |
| < 30 døgn                         | hver 6. time     |
| eldre (sovende/avsluttede serier) | én gang i døgnet |

Med tilfeldig jitter, slik at oppdateringer ikke klumper seg på hel time. En serie sjekkes dessuten bare så lenge noen faktisk abonnerer på den: serier uten forespørsler ryddes bort etter 90 dager.

## Belastningsbegrensende tiltak

- **Abonnent-oppslag når aldri NRK**: RSS-strømmene mellomlagres i egen database og på CDN-kant (ETag/304 mot klienter). Én serie koster NRK det samme med 1 eller 10 000 abonnenter.
- **Inkrementelle oppdateringer**: ved oppdatering hentes kun episodelisten (1 forespørsel); manifest-oppslag gjøres bare for episoder vi ikke har fra før.
- **Backoff for utilgjengelige episoder**: episoder uten spillbart manifest (geo-blokkert/utløpt) prøves på nytt med eksponentiell ventetid (1 → 30 døgn), ikke ved hver oppdatering.
- **Samtidighetstak**: maks 6 samtidige forespørsler ved interaktive oppslag, maks 3 under arkiv-innhenting.
- **Sammenslåing**: samtidige forespørsler om samme serie utløser ett NRK-oppslag, ikke flere.
- **Negativ mellomlagring**: oppslag på ukjente serie-ID-er huskes i 10 min, slik at feilstavinger og skanning ikke gir gjentatt trafikk.
- **Tidsavbrudd** på 15 s på alle forespørsler; feil håndteres med utsettelse, ikke aggressiv retry.

## Typisk fotavtrykk

For en typisk selvdriftet instans med noen titalls fulgte serier: **i størrelsesorden 50–200 forespørsler per døgn totalt** mot `psapi.nrk.no`, hvorav de fleste er enkle episodeliste-oppslag. En sovende serie koster ~2 forespørsler per døgn; en aktiv dagligserie ~24–48.

## Kontakt

Dersom NRK ønsker endringer i hvordan tjenesten bruker API-et — annen frekvens, identifiserende User-Agent, eller noe annet — [opprett en sak](../../issues), så ordner vi det.
