import { Head } from "fresh/runtime";
import { define } from "../utils.ts";

export default define.page(function Personvern() {
  return (
    <>
      <Head>
        <title>Personvern – Kringkasting</title>
      </Head>
      <article class="pt-16 pb-8 max-w-prose">
        <h1 class="text-4xl font-bold tracking-tight">Personvern</h1>
        <p class="mt-4 text-lg text-ink-2 dark:text-ink-2-dark">
          Kringkasting er laget for å samle inn så lite som overhodet mulig. Ingen kontoer, ingen sporing på tvers av
          nettsteder, ingenting selges eller deles.
        </p>

        <section class="mt-10 space-y-8 text-ink-2 dark:text-ink-2-dark">
          <div>
            <h2 class="text-xl font-semibold text-ink dark:text-ink-dark">Besøksstatistikk</h2>
            <p class="mt-2">
              Vi bruker en selvdriftet installasjon av{" "}
              <a href="https://umami.is" class="underline underline-offset-2">Umami</a>{" "}
              for anonym besøksstatistikk: sidevisninger, land, nettlesertype og enkelte anonyme hendelser (for eksempel
              hvilken podkast-app det abonneres med). Umami bruker ikke informasjonskapsler, lagrer ikke IP-adressen
              din, og kan ikke gjenkjenne deg mellom besøk. Dataene forlater aldri vår egen server.
            </p>
          </div>

          <div>
            <h2 class="text-xl font-semibold text-ink dark:text-ink-dark">Teknisk drift</h2>
            <p class="mt-2">
              IP-adressen din brukes flyktig i minnet til å begrense misbruk (rate-begrensning) og skrives aldri til
              disk. Nettstedet leveres via Cloudflare, som behandler trafikk som CDN. Valget ditt av podkast-app lagres
              kun i din egen nettleser (localStorage) og sendes aldri til oss.
            </p>
          </div>

          <div>
            <h2 class="text-xl font-semibold text-ink dark:text-ink-dark">Podkast-strømmene</h2>
            <p class="mt-2">
              Når du abonnerer på en strøm, henter podkast-appen din XML fra kringkast.ing, mens selve lyden og bildene
              hentes direkte fra NRK sine servere. For disse gjelder{" "}
              <a href="https://info.nrk.no/personvernerklaering/" class="underline underline-offset-2">
                NRK sin personvernerklæring
              </a>. Vi lagrer ingen data om hva du lytter til.
            </p>
          </div>

          <div>
            <h2 class="text-xl font-semibold text-ink dark:text-ink-dark">Spørsmål?</h2>
            <p class="mt-2">
              Tjenesten er åpen kildekode, så alt over kan etterprøves. Ta kontakt via{" "}
              <a href="https://github.com/matsbst/kringkasting" class="underline underline-offset-2">GitHub</a>.
            </p>
          </div>
        </section>
      </article>
    </>
  );
});
