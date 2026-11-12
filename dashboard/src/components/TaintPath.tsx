import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";

import { ObservedText } from "@/components/ObservedText";
import { TrustBadge } from "@/components/TrustBadge";
import type { PathNode } from "@/lib/api";
import { cn } from "@/lib/cn";
import { tierBorder } from "@/lib/trust";

/**
 * The path a value took, read left to right: the decision, the memory it came
 * from, and the source that memory came from. This is the claim the whole
 * product makes, so it gets a line of its own rather than a row in a table.
 */
export function TaintPath({ decisionLabel, nodes }: { decisionLabel: string; nodes: PathNode[] }) {
  return (
    <ol className="flex flex-wrap items-stretch gap-1.5" aria-label="Where this value came from">
      <Step title="this action" detail={decisionLabel} last={nodes.length === 0} />
      {nodes.map((node, index) => (
        <Step
          key={node.id}
          title={node.kind === "source" ? "came from" : "a memory"}
          detail={shortLabel(node.label)}
          full={node.label}
          text={node.text}
          tier={node.trust_min}
          reason={node.reason}
          to={
            node.kind === "source"
              ? { to: "/sources/$sourceId", params: { sourceId: node.id } }
              : node.memory_id
                ? { to: "/memories/$memoryId", params: { memoryId: node.memory_id } }
                : undefined
          }
          last={index === nodes.length - 1}
        />
      ))}
    </ol>
  );
}

type StepLink =
  | { to: "/sources/$sourceId"; params: { sourceId: string } }
  | { to: "/memories/$memoryId"; params: { memoryId: string } };

function Step({
  title,
  detail,
  full,
  text,
  tier,
  reason,
  to,
  last,
}: {
  title: string;
  detail: string;
  /** The exact label, for the tooltip, when `detail` is shortened. */
  full?: string;
  text?: string;
  tier?: number;
  reason?: string;
  to?: StepLink;
  last: boolean;
}) {
  const body = (
    <div
      className={cn(
        "flex h-full w-64 flex-col gap-1 rounded-md border bg-card px-3 py-2.5",
        tier === undefined ? "border-line" : tierBorder(tier),
      )}
    >
      <span className="text-xs text-ink-faint">{title}</span>
      <span className="line-clamp-2 font-mono text-xs break-all text-ink" title={full ?? detail}>
        {detail}
      </span>
      {text ? (
        <ObservedText
          text={truncate(text)}
          className="line-clamp-3 text-xs text-ink-faint"
          as="span"
        />
      ) : null}
      {tier !== undefined ? <TrustBadge trust={tier} reason={reason} className="mt-0.5" /> : null}
    </div>
  );
  return (
    <li className="flex items-stretch gap-1.5">
      {to ? (
        <Link {...to} className="transition-colors hover:brightness-125">
          {body}
        </Link>
      ) : (
        body
      )}
      {last ? null : (
        <ChevronRight className="size-3.5 shrink-0 self-center text-ink-faint" aria-hidden="true" />
      )}
    </li>
  );
}

/**
 * A memory arrives labelled "memory crm/memories/acme.billing_address v1". The
 * namespace is the same for every memory the agent has and the version is
 * rarely what you are looking at, so the card shows the part that tells one
 * memory from another and keeps the rest in the tooltip.
 */
function shortLabel(label: string): string {
  const memory = /^memory (\S+) (v\d+)$/.exec(label);
  if (!memory) return label;
  const key = memory[1]!;
  return key.slice(key.lastIndexOf("/") + 1);
}

function truncate(text: string, limit = 110): string {
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}
