import CopyButton from "../islands/CopyButton.tsx";
import { SearchResult } from "../lib/nrk/nrk.ts";
import { IconExternalLink, IconPodcast } from "./icons.tsx";

/** pick the image variant closest to the wanted rendered size */
function pickImage(images: SearchResult["images"], wantedWidth = 400) {
  if (!images || images.length === 0) {
    return null;
  }
  return images.reduce((best, candidate) =>
    Math.abs((candidate.width ?? 0) - wantedWidth) < Math.abs((best.width ?? 0) - wantedWidth) ? candidate : best
  );
}

export default function SeriesCard(props: { serie: SearchResult; origin: string }) {
  const feedUrl = new URL(`/api/feeds/${props.serie.seriesId}`, props.origin).toString();
  // prefer square (1:1) artwork over the default 16:9 images
  const image = pickImage(props.serie.images_1_1 ?? props.serie.images);
  const nrkUrl = `https://radio.nrk.no/podkast/${props.serie.seriesId}`;

  return (
    <article class="flex gap-4 sm:gap-5 p-4 sm:p-5 rounded-3xl bg-paper-raised dark:bg-paper-raised-dark border border-line dark:border-line-dark shadow-sm">
      {image && (
        <img
          src={image.uri}
          alt=""
          width={144}
          height={144}
          loading="lazy"
          class="size-24 sm:size-36 shrink-0 rounded-2xl object-cover shadow-md"
        />
      )}
      <div class="min-w-0 flex flex-col gap-2">
        <h3 class="font-display text-xl font-bold leading-snug">
          {props.serie.title}
        </h3>
        {props.serie.description && (
          <p class="text-sm text-ink-soft dark:text-ink-soft-dark line-clamp-2 sm:line-clamp-3">
            {props.serie.description}
          </p>
        )}
        <div class="mt-auto pt-2 flex flex-wrap items-center gap-2">
          <CopyButton copyText={feedUrl} label="Kopier RSS-lenke" />
          <a
            href={`podcast:${feedUrl}`}
            class="inline-flex items-center gap-2 rounded-full px-4 py-2 text-sm font-semibold border border-line dark:border-line-dark hover:border-accent hover:text-accent dark:hover:border-accent-dark dark:hover:text-accent-dark transition-colors"
          >
            <IconPodcast size={16} /> Åpne i podkast-app
          </a>
          <a
            href={nrkUrl}
            class="inline-flex items-center gap-1 px-2 py-2 text-sm text-ink-soft dark:text-ink-soft-dark hover:text-accent dark:hover:text-accent-dark transition-colors"
          >
            <IconExternalLink size={15} /> NRK
          </a>
        </div>
      </div>
    </article>
  );
}
