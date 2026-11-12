import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { useState } from "react";

import { ObservedText } from "@/components/ObservedText";
import { TrustBadge } from "@/components/TrustBadge";
import { Dot, Empty, ErrorNote, Panel, PanelHeader, SectionLabel, Spinner } from "@/components/ui";
import { api, type TimelineItem } from "@/lib/api";
import { formatOffset, formatTime, plural } from "@/lib/format";
import { tierTone } from "@/lib/trust";

/**
 * What kind of step this is. These are nouns, not verbs: the title beside them
 * already carries the verb, and "did Used web_fetch" reads like a stammer.
 */
const KIND_LABELS: Record<TimelineItem["kind"], string> = {
  source: "input",
  "memory.write": "memory",
  "memory.read": "recall",
  "llm.call": "model",
  decision: "action",
};

/**
 * Whether a step is part of the story or part of the plumbing.
 *
 * A conversation records far more than it reads as. Every turn asks the model,
 * every tool echoes its own output back, and the agent re-reads its memory
 * before each step. All of it is true and almost none of it is what someone
 * means when they ask what the assistant did, so the story is the default and
 * the rest is one click away.
 */
function isStory(item: TimelineItem): boolean {
  if (item.kind === "llm.call" || item.kind === "memory.read") return false;
  if (item.kind === "source") {
    const sourceKind = (item.meta?.kind ?? "") as string;
    return sourceKind !== "tool_output";
  }
  return true;
}

export function RunTimeline() {
  const { runId } = useParams({ from: "/runs/$runId" });
  const [everything, setEverything] = useState(false);
  const { data, isPending, error } = useQuery({
    queryKey: ["timeline", runId],
    queryFn: () => api.timeline(runId),
  });

  if (isPending) return <Spinner label="Loading…" />;
  if (error) {
    return (
      <ErrorNote title="This conversation could not be loaded." detail={(error as Error).message} />
    );
  }

  const items = everything ? data.items : data.items.filter(isStory);
  const hidden = data.items.length - items.length;

  return (
    <div className="mx-auto max-w-4xl space-y-2">
      <SectionLabel>Conversation</SectionLabel>
      <Panel>
        <PanelHeader
          title={
            data.opening ? (
              <ObservedText text={data.opening} as="span" />
            ) : (
              <span className="font-mono text-xs">{data.run.id}</span>
            )
          }
          subtitle={`${formatTime(data.run.started_at)}${
            data.run.scope ? ` · ${data.run.scope}` : ""
          } · ${data.run.agent_id}`}
          aside={
            hidden > 0 || everything ? (
              <button
                type="button"
                onClick={() => setEverything((on) => !on)}
                className="shrink-0 text-xs text-accent hover:underline"
              >
                {everything
                  ? "Show the story only"
                  : `Show every step (${plural(hidden, "more", "more")})`}
              </button>
            ) : undefined
          }
        />
        {items.length === 0 ? (
          <Empty>Nothing was recorded for this conversation.</Empty>
        ) : (
          <ol className="py-1">
            {items.map((item, index) => (
              <li key={`${item.kind}-${item.id}`} className="relative flex gap-3 px-4">
                {/* The spine: one conversation read top to bottom. */}
                <div className="relative flex w-3 shrink-0 justify-center">
                  {index > 0 ? (
                    <span className="absolute inset-x-1/2 top-0 h-4 w-px bg-line" />
                  ) : null}
                  {index < items.length - 1 ? (
                    <span className="absolute inset-x-1/2 top-4 bottom-0 w-px bg-line" />
                  ) : null}
                  <Dot
                    tone={item.tier ? tierTone(item.trust_min) : "muted"}
                    className="relative mt-[0.85rem]"
                  />
                </div>

                <div className="min-w-0 flex-1 py-2.5">
                  <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                    <span className="text-xs text-ink-faint">{KIND_LABELS[item.kind]}</span>
                    <ObservedText
                      text={item.title}
                      className="min-w-0 flex-1 text-sm text-ink"
                      as="span"
                    />
                    {item.tier ? <TrustBadge trust={item.trust_min} /> : null}
                    <time
                      className="shrink-0 font-mono text-xs text-ink-faint tabular-nums"
                      dateTime={item.at}
                      title={formatTime(item.at)}
                    >
                      {formatOffset(item.at, data.run.started_at)}
                    </time>
                  </div>
                  {item.text ? <ItemBody item={item} /> : null}
                  {item.link ? <ItemLink item={item} /> : null}
                </div>
              </li>
            ))}
          </ol>
        )}
      </Panel>
      <p className="px-1 text-xs text-ink-faint">
        Times are counted from the start of the conversation.
      </p>
    </div>
  );
}

/**
 * What a step carries. An action's arguments arrive as a JSON object, and
 * printing the raw object in the middle of a conversation is the fastest way
 * to make a page look like a log file, so they are laid out as a small table
 * of names and values. Everything else is shown as it was recorded.
 */
function ItemBody({ item }: { item: TimelineItem }) {
  const text = item.text ?? "";
  const args = item.kind === "decision" ? parseArgs(text) : null;
  if (!args) {
    return (
      <ObservedText
        text={text}
        className="mt-1.5 max-h-32 overflow-y-auto rounded-md bg-card px-2.5 py-2 font-mono text-xs text-ink-muted"
      />
    );
  }
  return (
    <dl className="mt-1.5 divide-y divide-line-soft rounded-md bg-card px-2.5">
      {args.map(([name, value]) => (
        <div key={name} className="flex flex-wrap items-baseline gap-x-3 py-1.5">
          <dt className="w-16 shrink-0 font-mono text-xs text-ink-faint">{name}</dt>
          <dd className="min-w-0 flex-1">
            <ObservedText text={value} className="text-xs text-ink-muted" as="span" />
          </dd>
        </div>
      ))}
    </dl>
  );
}

/** The argument names and values of a decision, or null if it is not one. */
function parseArgs(text: string): [string, string][] | null {
  try {
    const parsed: unknown = JSON.parse(text);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
    const entries = Object.entries(parsed as Record<string, unknown>);
    if (entries.length === 0) return null;
    return entries.map(([name, value]) => [
      name,
      typeof value === "string" ? value : JSON.stringify(value),
    ]);
  } catch {
    return null;
  }
}

function ItemLink({ item }: { item: TimelineItem }) {
  if (!item.link) return null;
  const className = "mt-1.5 inline-block text-xs text-accent hover:underline";
  switch (item.link.kind) {
    case "decision":
      return (
        <Link
          to="/decisions/$decisionId"
          params={{ decisionId: item.link.id }}
          className={className}
        >
          Where did this come from?
        </Link>
      );
    case "memory":
      return (
        <Link to="/memories/$memoryId" params={{ memoryId: item.link.id }} className={className}>
          Where did this memory come from?
        </Link>
      );
    default:
      return (
        <Link to="/sources/$sourceId" params={{ sourceId: item.link.id }} className={className}>
          See the whole thing
        </Link>
      );
  }
}
