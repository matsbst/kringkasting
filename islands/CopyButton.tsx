import { useState } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { IconCheck, IconCopy } from "../components/icons.tsx";

type Props = {
  copyText: string;
  label: string;
};

export default function CopyButton(props: Props) {
  const [copied, setCopied] = useState(false);

  const copy = () => {
    navigator.clipboard.writeText(props.copyText);
    setCopied(true);
    setTimeout(() => setCopied(false), 2_000);
  };

  return (
    <button
      type="button"
      onClick={copy}
      disabled={!IS_BROWSER}
      class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium bg-ink text-canvas hover:bg-ink/80 dark:bg-ink-dark dark:text-canvas-dark dark:hover:bg-ink-dark/85 transition-colors cursor-pointer"
    >
      {copied ? <IconCheck size={15} /> : <IconCopy size={15} />}
      {copied ? "Kopiert!" : props.label}
    </button>
  );
}
