import { cn } from "@/lib/cn";

/**
 * The effect and its 95% interval, on a scale from no change to a complete
 * change. An interval that touches zero is the honest way to say "this might
 * be nothing", so the zero line is always drawn.
 */
export function IntervalBar({
  effect,
  low,
  high,
  tone,
}: {
  effect: number;
  low: number;
  high: number;
  tone: "proven" | "unproven";
}) {
  const position = (value: number) => ((Math.min(1, Math.max(-1, value)) + 1) / 2) * 100;
  const left = position(low);
  const width = Math.max(1.5, position(high) - left);

  return (
    <div className="space-y-1">
      <div
        className="relative h-6 overflow-hidden rounded-md bg-card"
        role="img"
        aria-label={`Effect ${Math.round(effect * 100)} percent, 95% interval from ${Math.round(
          low * 100,
        )} to ${Math.round(high * 100)} percent`}
      >
        <div className="absolute inset-y-0 left-1/2 w-px bg-line" aria-hidden="true" />
        <div
          className={cn(
            "absolute inset-y-1.5 rounded-sm",
            tone === "proven" ? "bg-trust-3/30" : "bg-ink-faint/20",
          )}
          style={{ left: `${left}%`, width: `${width}%` }}
        />
        <div
          className={cn(
            "absolute inset-y-0 w-0.5",
            tone === "proven" ? "bg-trust-3" : "bg-ink-muted",
          )}
          style={{ left: `${position(effect)}%` }}
        />
      </div>
      <div className="flex justify-between font-mono text-xs text-ink-faint">
        <span>no change</span>
        <span>0</span>
        <span>always changes</span>
      </div>
    </div>
  );
}
