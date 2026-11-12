/**
 * Renders text the agent observed.
 *
 * Observed text is untrusted: it is never interpreted as HTML or markdown,
 * never executed, and the instructions inside it are never followed. React
 * escapes it, and this component adds the one piece of interpretation that is
 * safe and useful: a redaction placeholder becomes a chip, so the same value
 * can be recognised in a memory and in a tool argument without the value
 * itself ever being stored.
 */

import { cn } from "@/lib/cn";

const PLACEHOLDER = /\[(EMAIL|PHONE|CARD|KEY|JWT|TOKEN):([0-9a-f]{6})\]/g;

export function PlaceholderChip({ kind, digest }: { kind: string; digest: string }) {
  return (
    <span
      className="inline-flex items-baseline gap-1 rounded border border-accent/30 bg-accent/10 px-1 py-px align-baseline font-mono text-[0.9em] text-accent"
      title={`A ${kind.toLowerCase()} Totem did not store. The same value always shows this code, so you can see where it travelled.`}
    >
      <span className="text-[0.75em] tracking-wide opacity-60">{kind}</span>
      {digest}
    </span>
  );
}

export function ObservedText({
  text,
  className,
  as: Tag = "p",
}: {
  text: string;
  className?: string;
  as?: "p" | "span" | "div" | "pre";
}) {
  const parts: React.ReactNode[] = [];
  let cursor = 0;
  for (const match of text.matchAll(PLACEHOLDER)) {
    const start = match.index;
    if (start > cursor) parts.push(text.slice(cursor, start));
    parts.push(
      <PlaceholderChip key={`${start}-${match[2]}`} kind={match[1]!} digest={match[2]!} />,
    );
    cursor = start + match[0].length;
  }
  if (cursor < text.length) parts.push(text.slice(cursor));

  return <Tag className={cn("observed", className)}>{parts}</Tag>;
}
