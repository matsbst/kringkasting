/** Animated equalizer bars — a radio-flavored loading indicator. */
export default function SignalBars(props: { size?: number }) {
  const size = props.size ?? 18;
  const bars = [0, 1, 2, 3];
  // staggered so the four bars bounce out of phase
  const delays = ["0ms", "150ms", "300ms", "450ms"];
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      {bars.map((bar) => (
        <rect
          key={bar}
          class="eq-bar"
          style={{ transformOrigin: "center bottom", animationDelay: delays[bar] }}
          x={3 + bar * 5.2}
          y="4"
          width="3"
          height="16"
          rx="1.5"
          fill="currentColor"
        />
      ))}
    </svg>
  );
}
