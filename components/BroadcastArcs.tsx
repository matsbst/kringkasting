/**
 * Concentric broadcast arcs radiating from the page corner — the one
 * expressive element of the design, echoing what the product does.
 * Hairline ink strokes; the innermost arc carries the brand color.
 */
export default function BroadcastArcs(props: { class?: string }) {
  const radii = [88, 168, 248, 328, 408];
  return (
    <svg viewBox="0 0 480 480" fill="none" aria-hidden="true" class={props.class}>
      {radii.map((radius, index) => (
        <circle
          key={radius}
          cx="480"
          cy="0"
          r={radius}
          stroke={index === 0 ? "var(--color-brand)" : "currentColor"}
          stroke-width={index === 0 ? "2" : "1.25"}
        />
      ))}
    </svg>
  );
}
