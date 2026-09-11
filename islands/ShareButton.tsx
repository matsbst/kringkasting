import { useState } from "preact/hooks";
import { IconCheck, IconShare } from "../components/icons.tsx";

type Props = {
  title: string;
  url: string;
};

/** native share sheet where available (mobile), copy-link fallback elsewhere */
export default function ShareButton(props: Props) {
  const [copied, setCopied] = useState(false);

  const share = async () => {
    if (navigator.share) {
      try {
        await navigator.share({ title: props.title, url: props.url });
      } catch {
        // user dismissed the sheet; nothing to do
      }
      return;
    }
    try {
      await navigator.clipboard.writeText(props.url);
      setCopied(true);
      setTimeout(() => setCopied(false), 1_500);
    } catch {
      // clipboard unavailable: fall back to the native prompt-free path
      console.error("clipboard unavailable");
    }
  };

  return (
    <button
      type="button"
      onClick={share}
      class="inline-flex items-center gap-1.5 px-1.5 py-1.5 text-sm text-ink-2 dark:text-ink-2-dark hover:text-ink dark:hover:text-ink-dark transition-colors cursor-pointer"
    >
      {copied ? <IconCheck size={15} /> : <IconShare size={15} />}
      {copied ? "Kopiert" : "Del"}
    </button>
  );
}
