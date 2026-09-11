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
      class={`inline-flex items-center gap-2 rounded-full px-4 py-2 text-sm font-semibold transition-colors cursor-pointer
        ${
        copied
          ? "bg-green-700 text-white"
          : "bg-accent hover:bg-accent-strong text-white dark:bg-accent-dark dark:text-paper-dark dark:hover:bg-accent"
      }`}
    >
      {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
      {copied ? "Kopiert!" : props.label}
    </button>
  );
}
