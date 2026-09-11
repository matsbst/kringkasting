import CopyButton from "../islands/CopyButton.tsx";
import type { SeriesSummary } from "../lib/series-summary.ts";
import { IconPodcast } from "./icons.tsx";

/** one-tap subscribe deep links for popular podcast apps */
function subscribeLinks(feedUrl: string) {
  const withoutScheme = feedUrl.replace(/^https?:\/\//, "");
  const encoded = encodeURIComponent(feedUrl);
  return [
    { name: "Apple Podcasts", href: `podcast://${withoutScheme}` },
    { name: "Overcast", href: `overcast://x-callback-url/add?url=${encoded}` },
    { name: "Pocket Casts", href: `pktc://subscribe/${withoutScheme}` },
    { name: "Castro", href: `castro://subscribe/${withoutScheme}` },
    { name: "AntennaPod", href: `https://antennapod.org/deeplink/subscribe?url=${encoded}` },
  ];
}

export default function SeriesCard(props: { serie: SeriesSummary; origin: string }) {
  const feedUrl = new URL(`/api/feeds/${props.serie.seriesId}`, props.origin).toString();
  const nrkUrl = `https://radio.nrk.no/podkast/${props.serie.seriesId}`;

  return (
    <article class="flex gap-5">
      {props.serie.imageUrl && (
        <img
          src={props.serie.imageUrl}
          alt=""
          width={112}
          height={112}
          loading="lazy"
          class="size-20 sm:size-28 shrink-0 rounded-lg object-cover"
        />
      )}
      <div class="min-w-0 flex flex-col">
        <h3 class="text-lg font-medium leading-snug">
          {props.serie.title}
        </h3>
        {props.serie.description && (
          <p class="mt-1 text-sm text-ink-2 dark:text-ink-2-dark line-clamp-2">
            {props.serie.description}
          </p>
        )}
        <div class="mt-3 flex flex-wrap items-center gap-x-2 gap-y-2">
          <CopyButton copyText={feedUrl} label="Kopier RSS-lenke" />
          <details class="relative">
            <summary class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium border border-line-strong dark:border-line-strong-dark hover:bg-hover dark:hover:bg-hover-dark transition-colors cursor-pointer select-none list-none [&::-webkit-details-marker]:hidden">
              <IconPodcast size={15} /> Abonner i app
            </summary>
            <ul class="absolute z-10 mt-1.5 min-w-44 rounded-lg bg-canvas dark:bg-canvas-dark border border-line-strong dark:border-line-strong-dark shadow-[0_2px_8px_rgb(0_0_0_/_6%)] overflow-hidden">
              {subscribeLinks(feedUrl).map((app) => (
                <li key={app.name}>
                  <a
                    href={app.href}
                    class="block px-3.5 py-2 text-sm hover:bg-hover dark:hover:bg-hover-dark transition-colors"
                  >
                    {app.name}
                  </a>
                </li>
              ))}
            </ul>
          </details>
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
