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
          content="Åpne RSS-strømmer for NRK sine podkaster – hør dem i akkurat den podkast-appen du selv vil."
        />
        <link
          rel="icon"
          href="data:image/svg+xml,<svg xmlns=%22http://www.w3.org/2000/svg%22 viewBox=%220 0 100 100%22><text y=%22.9em%22 font-size=%2290%22>📻</text></svg>"
        />
        <title>NRSS – NRK-podkaster som RSS</title>
      </head>
      <body class="min-h-screen flex flex-col bg-paper text-ink dark:bg-paper-dark dark:text-ink-dark antialiased">
        <header class="px-6 pt-8 pb-2">
          <a href="/" class="flex items-center justify-center gap-3 group">
            <span class="grid place-items-center size-11 rounded-2xl bg-accent text-white shadow-md shadow-accent/30 group-hover:rotate-6 transition-transform">
              <IconRss size={24} />
            </span>
            <span class="font-display text-3xl font-bold tracking-tight">NRSS</span>
          </a>
        </header>

        <main class="flex-1 w-full max-w-3xl mx-auto px-6">
          <Component />
        </main>

        <footer class="mt-16 border-t border-line dark:border-line-dark">
          <div class="max-w-3xl mx-auto px-6 py-8 text-sm text-ink-soft dark:text-ink-soft-dark space-y-2">
            <p>
              NRSS er ikke tilknyttet NRK. Alt innhold i strømmene tilhører NRK og hentes fra deres åpne API.
            </p>
            <p>
              <a
                href="https://github.com/matsbst/nrss"
                class="underline decoration-line dark:decoration-line-dark underline-offset-4 hover:text-accent dark:hover:text-accent-dark"
              >
                Kildekode
              </a>{" "}
              · basert på{" "}
              <a
                href="https://github.com/olaven/nrss"
                class="underline decoration-line dark:decoration-line-dark underline-offset-4 hover:text-accent dark:hover:text-accent-dark"
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
