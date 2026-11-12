import { Dot } from "@/components/ui";
import { cn } from "@/lib/cn";
import { TIER_MEANINGS, TIER_NAMES, TIER_WORDS, tierOf, tierText, tierTone } from "@/lib/trust";

/**
 * Where a piece of information came from, as a dot and a few words.
 *
 * The visible label is plain English, because this is the first thing a
 * newcomer has to understand and "T1 External" teaches them nothing. The exact
 * tier name is in the tooltip, for anyone who needs to match it to the docs.
 *
 * The same five colours mean the same five things on every screen, so a glance
 * down a list finds the things that came from outside.
 */
export function TrustBadge({
  trust,
  label,
  reason,
  className,
}: {
  trust: number | null | undefined;
  /** Overrides the plain label. Rarely wanted: the tier already says it. */
  label?: string;
  reason?: string;
  className?: string;
}) {
  const tier = tierOf(trust);
  const meaning = `${TIER_NAMES[tier]}. ${TIER_MEANINGS[tier]}`;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-xs whitespace-nowrap",
        tierText(trust),
        className,
      )}
      title={reason ? `${meaning} ${reason}` : meaning}
    >
      <Dot tone={tierTone(trust)} />
      {label ?? TIER_WORDS[tier]}
    </span>
  );
}
