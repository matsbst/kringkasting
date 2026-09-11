import ShareButton from "../islands/ShareButton.tsx";
import SubscribeButton from "../islands/SubscribeButton.tsx";
import type { SeriesSummary } from "../lib/series-summary.ts";

export default function SeriesCard(props: { serie: SeriesSummary; origin: string }) {
  const feedUrl = new URL(`/api/feeds/${props.serie.seriesId}`, props.origin).toString();
  const nrkUrl = `https://radio.nrk.no/podkast/${props.serie.seriesId}`;

  return (
    <article class="flex gap-5">
      {props.serie.imageUrl && (
        <img
          src={props.serie.imageUrl}
          alt=""
          width={128}
          height={128}
          loading="lazy"
          class="size-24 sm:size-32 shrink-0 rounded-xl object-cover"
        />
      )}
      <div class="min-w-0 flex flex-col">
        <h3 class="text-xl font-semibold leading-snug tracking-tight">
          {props.serie.title}
        </h3>
        {props.serie.description && (
          <p class="mt-1 text-sm text-ink-2 dark:text-ink-2-dark line-clamp-2">
            {props.serie.description}
          </p>
        )}
        <div class="mt-3 flex flex-wrap items-center gap-2">
          <SubscribeButton feedUrl={feedUrl} />
          <ShareButton
            title={props.serie.title}
            url={new URL(`/?query=${encodeURIComponent(props.serie.title)}`, props.origin).toString()}
          />
          <a
            href={nrkUrl}
            class="px-1.5 py-1.5 text-sm text-ink-2 dark:text-ink-2-dark underline underline-offset-2 hover:text-ink dark:hover:text-ink-dark transition-colors"
          >
            Hos NRK
          </a>
        </div>
      </div>
    </article>
  );
}
