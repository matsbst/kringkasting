import { define } from "../utils.ts";
import { getOrigin } from "../lib/utils.ts";
import { IconRss } from "../components/icons.tsx";
import BroadcastArcs from "../components/BroadcastArcs.tsx";

const DESCRIPTION =
  "Åpne RSS-strømmer for NRK sine podkaster, med komplette episodearkiv – hør dem i akkurat den podkast-appen du selv vil.";

export default define.page(function App(ctx) {
  const { Component } = ctx;
  const origin = getOrigin(ctx.req);
  const canonical = `${origin}${ctx.url.pathname}`;
  return (
    <html lang="no">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <meta name="description" content={DESCRIPTION} />
        <title>Kringkasting – NRK-podkaster som RSS</title>
        <link rel="canonical" href={canonical} />
        <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
        <link rel="icon" href="/favicon.ico" sizes="32x32" />
        <link rel="icon" type="image/png" sizes="32x32" href="/favicon-32.png" />
        <link rel="icon" type="image/png" sizes="512x512" href="/icon-512.png" />
        <link rel="apple-touch-icon" href="/apple-touch-icon.png" />
        <link rel="manifest" href="/manifest.json" />
        <meta property="og:site_name" content="Kringkasting" />
        <meta property="og:type" content="website" />
        <meta property="og:title" content="Kringkasting – NRK-podkaster som RSS" />
        <meta property="og:description" content={DESCRIPTION} />
        <meta property="og:url" content={canonical} />
        <meta property="og:image" content={`${origin}/og.png`} />
        <meta property="og:image:width" content="1200" />
        <meta property="og:image:height" content="630" />
        <meta property="og:locale" content="nb_NO" />
        <meta name="twitter:card" content="summary_large_image" />
        <meta name="twitter:title" content="Kringkasting – NRK-podkaster som RSS" />
        <meta name="twitter:description" content={DESCRIPTION} />
        <meta name="twitter:image" content={`${origin}/og.png`} />
        {/* self-hosted, cookieless Umami analytics */}
        <script defer src="https://data.i1.no/script.js" data-website-id="e70aeb45-6ad7-4d32-bc32-2bbeeff91ed0" />
      </head>
      <body class="relative min-h-screen flex flex-col overflow-x-clip bg-canvas text-ink dark:bg-canvas-dark dark:text-ink-dark antialiased">
        <a href="#innhold" class="skip-link">Hopp til innhold</a>
        {/* broadcast arcs radiating from the page's own corner */}
        <BroadcastArcs class="pointer-events-none absolute top-0 right-0 w-72 sm:w-[27rem] text-ink/10 dark:text-ink-dark/15" />
        <header class="w-full max-w-2xl mx-auto px-6 pt-10">
          <a href="/" class="logo-ping inline-flex items-center gap-2">
            {
              /* the bare broadcast glyph is the page's color signature;
                it pings a ring outward on hover */
            }
            <span class="relative grid place-items-center">
              <IconRss size={24} class="text-brand" stroke-width="2.5" />
              <span
                aria-hidden="true"
                class="logo-ring pointer-events-none absolute inset-0 rounded-full border border-brand opacity-0"
              />
            </span>
            <span class="text-xl font-bold tracking-tight">kringkasting</span>
          </a>
        </header>

        <main id="innhold" class="flex-1 w-full max-w-2xl mx-auto px-6">
          <Component />
        </main>

        <footer class="w-full max-w-2xl mx-auto px-6 mt-24 mb-10">
          <div class="border-t border-line dark:border-line-dark pt-6 text-sm text-ink-2 dark:text-ink-2-dark space-y-1.5">
            <p>
              Kringkasting er ikke tilknyttet NRK. Alt innhold i strømmene tilhører NRK og hentes fra deres åpne API.
              Kringkasting lagrer ingen data om deg.
            </p>
            <p>
              Kringkasting er{" "}
              <a
                href="https://github.com/matsbst/kringkasting"
                class="underline underline-offset-2 hover:text-ink dark:hover:text-ink-dark"
              >
                åpen kildekode
              </a>{" "}
              – basert på{" "}
              <a
                href="https://github.com/olaven/nrss"
                class="underline underline-offset-2 hover:text-ink dark:hover:text-ink-dark"
              >
                NRSS
              </a>{" "}
              ·{" "}
              <a href="/personvern" class="underline underline-offset-2 hover:text-ink dark:hover:text-ink-dark">
                Personvern
              </a>{" "}
              ·{" "}
              <a
                href="https://github.com/matsbst/kringkasting/issues/new"
                class="underline underline-offset-2 hover:text-ink dark:hover:text-ink-dark"
              >
                Funnet en feil?
              </a>
            </p>
          </div>
        </footer>
      </body>
    </html>
  );
});
