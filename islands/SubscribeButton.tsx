import { ComponentChildren } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { IconCheck, IconChevronDown, IconCopy, IconPodcast } from "../components/icons.tsx";
import {
  AntennaPodIcon,
  ApplePodcastsIcon,
  CastroIcon,
  DowncastIcon,
  OvercastIcon,
  PocketCastsIcon,
  PodcastAddictIcon,
  SpotifyIcon,
  YouTubeMusicIcon,
} from "../components/app-icons.tsx";

const STORAGE_KEY = "kringkasting:podcast-app";
const CHANGE_EVENT = "kringkasting:podcast-app-changed";

type PodcastApp = {
  id: string;
  name: string;
  icon: (props: { size?: number }) => ComponentChildren;
  href: (feedUrl: string) => string;
};

function withoutScheme(feedUrl: string) {
  return feedUrl.replace(/^https?:\/\//, "");
}

/** RFC 4648 §5 base64url, the encoding YouTube Music expects */
function base64Url(value: string) {
  return btoa(value).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

const APPS: PodcastApp[] = [
  { id: "apple", name: "Apple Podcasts", icon: ApplePodcastsIcon, href: (url) => `podcast://${withoutScheme(url)}` },
  {
    id: "overcast",
    name: "Overcast",
    icon: OvercastIcon,
    href: (url) => `overcast://x-callback-url/add?url=${encodeURIComponent(url)}`,
  },
  {
    id: "pocketcasts",
    name: "Pocket Casts",
    icon: PocketCastsIcon,
    href: (url) => `pktc://subscribe/${withoutScheme(url)}`,
  },
  { id: "castro", name: "Castro", icon: CastroIcon, href: (url) => `castro://subscribe/${withoutScheme(url)}` },
  { id: "downcast", name: "Downcast", icon: DowncastIcon, href: (url) => `downcast://${withoutScheme(url)}` },
  {
    id: "podcastaddict",
    name: "Podcast Addict",
    icon: PodcastAddictIcon,
    href: (url) => `podcastaddict://${withoutScheme(url)}`,
  },
  {
    id: "antennapod",
    name: "AntennaPod",
    icon: AntennaPodIcon,
    href: (url) => `https://antennapod.org/deeplink/subscribe?url=${encodeURIComponent(url)}`,
  },
  {
    id: "youtubemusic",
    name: "YouTube Music",
    icon: YouTubeMusicIcon,
    href: (url) => `https://music.youtube.com/library/podcasts?addrssfeed=${base64Url(url)}`,
  },
];

/**
 * Subscribe control. First use opens a chooser (bottom sheet on mobile,
 * centered panel on desktop, via native <dialog>); the chosen app is
 * remembered in localStorage so later visits get a one-tap
 * "Åpne i <app>" with a small chevron to switch apps.
 */
export default function SubscribeButton(props: { feedUrl: string }) {
  const [chosenId, setChosenId] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  // incremented per tap; keyed span re-runs the pulse-ring animation
  const [pulse, setPulse] = useState(0);
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    setChosenId(localStorage.getItem(STORAGE_KEY));
    // keep every card's button in sync when a choice is made anywhere
    const sync = () => setChosenId(localStorage.getItem(STORAGE_KEY));
    addEventListener(CHANGE_EVENT, sync);
    return () => removeEventListener(CHANGE_EVENT, sync);
  }, []);

  const chosen = APPS.find((app) => app.id === chosenId) ?? null;

  const open = () => {
    setCopied(false);
    dialogRef.current?.showModal();
  };

  const close = () => dialogRef.current?.close();

  const remember = (app: PodcastApp) => {
    localStorage.setItem(STORAGE_KEY, app.id);
    dispatchEvent(new Event(CHANGE_EVENT));
    close();
    // the row is an anchor; navigation to the deep link continues natively
  };

  const copyLink = () => {
    navigator.clipboard.writeText(props.feedUrl);
    setCopied(true);
    setTimeout(() => {
      close();
      setCopied(false);
    }, 900);
  };

  const primaryButton =
    "relative inline-flex items-center gap-1.5 rounded-lg text-sm font-semibold bg-ink text-canvas hover:bg-ink/80 dark:bg-ink-dark dark:text-canvas-dark dark:hover:bg-ink-dark/85 transition-colors cursor-pointer";
  const pulseRing = pulse > 0 && (
    <span key={pulse} class="pulse-ring border-ink/60 dark:border-ink-dark/60" aria-hidden="true" />
  );

  return (
    <>
      {chosen
        ? (
          <span class="inline-flex">
            <a
              href={chosen.href(props.feedUrl)}
              onClick={() => setPulse((count) => count + 1)}
              class={`${primaryButton} rounded-r-none px-3 py-2`}
            >
              <chosen.icon size={15} /> Åpne i {chosen.name}
              {pulseRing}
            </a>
            <button
              type="button"
              onClick={open}
              aria-label="Velg en annen podkast-app"
              class={`${primaryButton} rounded-l-none border-l border-canvas/25 dark:border-canvas-dark/25 px-2 py-2`}
            >
              <IconChevronDown size={15} />
            </button>
          </span>
        )
        : (
          <button
            type="button"
            onClick={() => {
              setPulse((count) => count + 1);
              open();
            }}
            class={`${primaryButton} px-3 py-2`}
          >
            <IconPodcast size={15} /> Abonner i app
            {pulseRing}
          </button>
        )}

      <dialog
        ref={dialogRef}
        class="sheet fixed inset-x-0 bottom-0 top-auto m-0 w-full max-w-full rounded-t-2xl sm:inset-0 sm:m-auto sm:h-fit sm:w-96 sm:rounded-xl border-0 sm:border sm:border-line-strong sm:dark:border-line-strong-dark p-0 bg-canvas dark:bg-canvas-dark text-ink dark:text-ink-dark"
        onClick={(event) => {
          // a click on the backdrop targets the dialog element itself
          if (event.target === dialogRef.current) {
            close();
          }
        }}
      >
        <div class="p-2 pb-[max(0.5rem,env(safe-area-inset-bottom))]">
          <h2 class="px-3 pt-3 pb-2 text-sm font-semibold text-ink-2 dark:text-ink-2-dark">
            Velg podkast-app
          </h2>
          <ul>
            {APPS.map((app) => (
              <li key={app.id}>
                <a
                  href={app.href(props.feedUrl)}
                  onClick={() => remember(app)}
                  class="flex items-center gap-3 min-h-12 px-3 rounded-lg text-base hover:bg-hover dark:hover:bg-hover-dark transition-colors"
                >
                  <app.icon size={20} />
                  {app.name}
                </a>
              </li>
            ))}
            {/* Spotify doesn't let listeners add RSS feeds; say so where people will look for it */}
            <li aria-disabled="true" class="flex items-center gap-3 min-h-12 px-3 select-none">
              <span class="opacity-45">
                <SpotifyIcon size={20} />
              </span>
              <span class="text-base text-ink-3 dark:text-ink-3-dark">Spotify</span>
              <span class="ml-auto text-sm text-ink-2 dark:text-ink-2-dark">støtter ikke RSS</span>
            </li>
            <li class="mt-1 border-t border-line dark:border-line-dark pt-1">
              <button
                type="button"
                onClick={copyLink}
                class="flex w-full items-center gap-3 min-h-12 px-3 rounded-lg text-base hover:bg-hover dark:hover:bg-hover-dark transition-colors cursor-pointer"
              >
                {copied ? <IconCheck size={20} /> : <IconCopy size={20} />}
                {copied ? "Kopiert!" : "Kopier RSS-lenke"}
              </button>
            </li>
          </ul>
          <button
            type="button"
            onClick={close}
            class="mt-1 flex w-full items-center justify-center min-h-12 rounded-lg text-base font-medium text-ink-2 dark:text-ink-2-dark hover:bg-hover dark:hover:bg-hover-dark transition-colors cursor-pointer"
          >
            Avbryt
          </button>
        </div>
      </dialog>
    </>
  );
}
