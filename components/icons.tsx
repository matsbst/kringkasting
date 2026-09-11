/**
 * Inline icons (from tabler.io, MIT licensed), replacing the previous
 * https://deno.land/x/tabler_icons_tsx dependency.
 */

import { JSX } from "preact";

type IconProps = JSX.SVGAttributes<SVGSVGElement> & { size?: number };

function base(props: IconProps) {
  const { size = 20, ...rest } = props;
  return {
    xmlns: "http://www.w3.org/2000/svg",
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    "stroke-width": "2",
    "stroke-linecap": "round" as const,
    "stroke-linejoin": "round" as const,
    "aria-hidden": true,
    ...rest,
  };
}

export function IconSearch(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M10 10m-7 0a7 7 0 1 0 14 0a7 7 0 1 0 -14 0" />
      <path d="M21 21l-6 -6" />
    </svg>
  );
}

export function IconCopy(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M7 7m0 2.667a2.667 2.667 0 0 1 2.667 -2.667h8.666a2.667 2.667 0 0 1 2.667 2.667v8.666a2.667 2.667 0 0 1 -2.667 2.667h-8.666a2.667 2.667 0 0 1 -2.667 -2.667z" />
      <path d="M4.012 16.737a2.005 2.005 0 0 1 -1.012 -1.737v-10c0 -1.1 .9 -2 2 -2h10c.75 0 1.158 .385 1.5 1" />
    </svg>
  );
}

export function IconCheck(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M5 12l5 5l10 -10" />
    </svg>
  );
}

export function IconRss(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M5 19m-1 0a1 1 0 1 0 2 0a1 1 0 1 0 -2 0" />
      <path d="M4 4a16 16 0 0 1 16 16" />
      <path d="M4 11a9 9 0 0 1 9 9" />
    </svg>
  );
}

export function IconPodcast(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M19 10a7 7 0 1 0 -14 0a6.97 6.97 0 0 0 1.626 4.486" />
      <path d="M12 10m-2 0a2 2 0 1 0 4 0a2 2 0 1 0 -4 0" />
      <path d="M9.5 15a3.5 3.5 0 0 1 5 0l-.25 6.5a2 2 0 0 1 -2 1.5h-.5a2 2 0 0 1 -2 -1.5z" />
    </svg>
  );
}

export function IconExternalLink(props: IconProps) {
  return (
    <svg {...base(props)}>
      <path d="M12 6h-6a2 2 0 0 0 -2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2 -2v-6" />
      <path d="M11 13l9 -9" />
      <path d="M15 4h5v5" />
    </svg>
  );
}
