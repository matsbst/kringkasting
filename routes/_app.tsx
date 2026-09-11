import { define } from "../utils.ts";
import { IconRss } from "../components/icons.tsx";

export default define.page(function App({ Component }) {
  return (
    <html lang="no">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <meta
          name="description"
          content="Åpne RSS-strømmer for NRK sine podkaster, med komplette episodearkiv – hør dem i akkurat den podkast-appen du selv vil."
        />
        <link
          rel="icon"
          href="data:image/svg+xml,<svg xmlns=%22http://www.w3.org/2000/svg%22 viewBox=%220 0 100 100%22><text y=%22.9em%22 font-size=%2290%22>📻</text></svg>"
        />
        <title>Kringkasting – NRK-podkaster som RSS</title>
      </head>
      <body class="min-h-screen flex flex-col bg-canvas text-ink dark:bg-canvas-dark dark:text-ink-dark antialiased">
        <header class="w-full max-w-2xl mx-auto px-6 pt-10">
          <a href="/" class="inline-flex items-center gap-2.5">
            {/* the brand mark keeps its color; everything else stays neutral */}
            <span class="grid place-items-center size-7 rounded-md bg-brand text-white">
              <IconRss size={17} />
            </span>
            <span class="text-lg font-semibold tracking-tight">Kringkasting</span>
          </a>
        </header>

        <main class="flex-1 w-full max-w-2xl mx-auto px-6">
          <Component />
        </main>

        <footer class="w-full max-w-2xl mx-auto px-6 mt-24 mb-10">
          <div class="border-t border-line dark:border-line-dark pt-6 text-sm text-ink-2 dark:text-ink-2-dark space-y-1.5">
            <p>
              Kringkasting er ikke tilknyttet NRK. Alt innhold i strømmene tilhører NRK og hentes fra deres åpne API.
            </p>
            <p>
              <a
                href="https://github.com/matsbst/kringkasting"
                class="underline underline-offset-2 hover:text-ink dark:hover:text-ink-dark"
              >
                Kildekode
              </a>{" "}
              · basert på{" "}
              <a
                href="https://github.com/olaven/nrss"
                class="underline underline-offset-2 hover:text-ink dark:hover:text-ink-dark"
              >
                NRSS av Olav Sundfør
              </a>
            </p>
          </div>
        </footer>
      </body>
    </html>
  );
});
