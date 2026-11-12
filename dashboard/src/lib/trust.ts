/** The trust tiers, as the server numbers them. */
export type Tier = 0 | 1 | 2 | 3 | 4;

/**
 * What a tier is called in the product's own vocabulary. It is exact and it is
 * jargon, so it belongs in a tooltip, not on the page.
 */
export const TIER_NAMES: Record<Tier, string> = {
  0: "T0 Unknown",
  1: "T1 External",
  2: "T2 Tool",
  3: "T3 User",
  4: "T4 System",
};

/**
 * What a tier is called on the page. A trust tier is only ever an answer to
 * "where did this come from", so that is what the label says. Someone reading
 * their first alert should not have to learn a numbering first.
 */
export const TIER_WORDS: Record<Tier, string> = {
  0: "Unknown origin",
  1: "From outside",
  2: "From a tool",
  3: "From you",
  4: "Built in",
};

/** What each tier means, in one line, for the tooltip. */
export const TIER_MEANINGS: Record<Tier, string> = {
  0: "Totem has no record of where this came from.",
  1: "This came from outside: a page the assistant read, or a tool you have not vouched for.",
  2: "This came from one of your own tools.",
  3: "You said this.",
  4: "This is the assistant's own configuration.",
};

/** The page label for a tier, with the exact name for the tooltip. */
export function tierWords(value: number | null | undefined): string {
  return TIER_WORDS[tierOf(value)];
}

const TONES = ["trust0", "trust1", "trust2", "trust3", "trust4"] as const;
const TEXT: Record<Tier, string> = {
  0: "text-trust-0",
  1: "text-trust-1",
  2: "text-trust-2",
  3: "text-trust-3",
  4: "text-trust-4",
};
const BORDER: Record<Tier, string> = {
  0: "border-trust-0/35",
  1: "border-trust-1/35",
  2: "border-trust-2/35",
  3: "border-trust-3/35",
  4: "border-trust-4/35",
};

export function tierOf(value: number | null | undefined): Tier {
  if (value === null || value === undefined) return 0;
  return Math.min(4, Math.max(0, Math.round(value))) as Tier;
}

export function tierTone(value: number | null | undefined): (typeof TONES)[number] {
  return TONES[tierOf(value)]!;
}

export function tierText(value: number | null | undefined): string {
  return TEXT[tierOf(value)];
}

export function tierBorder(value: number | null | undefined): string {
  return BORDER[tierOf(value)];
}

/** True when a tier is untrusted, which is what an alert is about. */
export function isUntrusted(value: number | null | undefined): boolean {
  return tierOf(value) <= 1;
}

export type Severity = "critical" | "high" | "medium" | "low";

export const SEVERITY_TONE: Record<string, "trust0" | "trust1" | "trust2" | "muted"> = {
  critical: "trust0",
  high: "trust1",
  medium: "trust2",
  low: "muted",
};

export const SEVERITY_TEXT: Record<string, string> = {
  critical: "text-trust-0",
  high: "text-trust-1",
  medium: "text-trust-2",
  low: "text-ink-muted",
};
