import { HttpError } from "fresh";
import { define } from "../utils.ts";

export default define.page(function ErrorPage(ctx) {
  const notFound = ctx.error instanceof HttpError && ctx.error.status === 404;

  return (
    <section class="pt-20 pb-12">
      <p class="text-6xl font-extralight text-ink-3 dark:text-ink-3-dark tabular-nums leading-none">
        {notFound ? "404" : "Oi!"}
      </p>
      <h1 class="mt-6 text-4xl font-bold tracking-tight">
        {notFound ? "Fant ikke siden" : "Noe gikk galt"}
      </h1>
      <p class="mt-4 text-lg text-ink-2 dark:text-ink-2-dark max-w-lg">
        {notFound
          ? "Siden du leter etter finnes ikke. Prøv å søke etter podkasten i stedet."
          : "Prøv igjen om et lite øyeblikk."}
      </p>
      <form action="/" method="get" class="mt-8 max-w-md">
        <label class="sr-only" htmlFor="query">Søk etter NRK-podkast</label>
        <input
          type="search"
          id="query"
          name="query"
          placeholder="Søk etter en NRK-podkast"
          class="w-full h-12 rounded-lg border-2 border-line dark:border-line-dark bg-canvas dark:bg-canvas-dark px-4 text-base placeholder:text-ink-3 dark:placeholder:text-ink-3-dark focus:outline-none focus:border-ink dark:focus:border-ink-dark"
        />
      </form>
      <a
        href="/"
        class="mt-6 inline-block text-sm underline underline-offset-2 text-ink-2 dark:text-ink-2-dark hover:text-ink dark:hover:text-ink-dark"
      >
        Til forsiden
      </a>
    </section>
  );
});
