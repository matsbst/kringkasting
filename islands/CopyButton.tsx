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
      class="inline-flex items-center gap-1.5 rounded-lg px-3 py-2 text-sm font-medium border border-line-strong dark:border-line-strong-dark hover:bg-hover dark:hover:bg-hover-dark transition-colors cursor-pointer"
    >
      {copied ? <IconCheck size={15} /> : <IconCopy size={15} />}
      {copied ? "Kopiert!" : props.label}
    </button>
  );
}
