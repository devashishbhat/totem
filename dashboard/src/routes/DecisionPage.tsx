import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { Check } from "lucide-react";

import { IncidentGraph } from "@/components/IncidentGraph";
import { ObservedText, PlaceholderChip } from "@/components/ObservedText";
import { ReplayCard } from "@/components/ReplayCard";
import { TaintPath } from "@/components/TaintPath";
import { TrustBadge } from "@/components/TrustBadge";
import {
  Dot,
  Empty,
  ErrorNote,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  SectionLabel,
  Spinner,
} from "@/components/ui";
import { api, type Influence } from "@/lib/api";
import { cn } from "@/lib/cn";
import { describeAlert, shortKey } from "@/lib/describe";
import { formatTime } from "@/lib/format";
import { SEVERITY_TEXT, SEVERITY_TONE, tierBorder } from "@/lib/trust";

/** What a source is, for a reader who did not write the agent. */
const SOURCE_KINDS: Record<string, string> = {
  user_message: "what you said",
  system_config: "the assistant's own instructions",
  tool_output: "a tool's reply",
  external_document: "a page it read",
  unknown: "something of unknown origin",
};

const PLACEHOLDER_ONLY = /^\[(EMAIL|PHONE|CARD|KEY|JWT|TOKEN):([0-9a-f]{6})\]$/;

const LEVEL_ORDER: Influence["level"][] = [
  "counterfactual",
  "value_flow",
  "in_context",
  "retrieved",
];

/** The hero page: what the agent did, and why it did it. */
export function DecisionPage() {
  const { decisionId } = useParams({ from: "/decisions/$decisionId" });
  const { data, isPending, error } = useQuery({
    queryKey: ["explain", decisionId],
    queryFn: () => api.explain(decisionId),
  });

  if (isPending) return <Spinner label="Reconstructing the decision…" />;
  if (error) {
    return (
      <ErrorNote title="This decision could not be loaded." detail={(error as Error).message} />
    );
  }

  const { decision } = data;
  const strongest = [...data.memories].sort(
    (a, b) => LEVEL_ORDER.indexOf(a.level) - LEVEL_ORDER.indexOf(b.level),
  )[0];

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      {data.alerts.map((alert) => (
        <div
          key={alert.id}
          className="flex items-center gap-2.5 rounded-lg border border-trust-0/30 bg-trust-0/[0.06] px-4 py-3"
        >
          <Dot tone={SEVERITY_TONE[alert.severity] ?? "muted"} />
          <span
            className={cn("text-xs font-medium", SEVERITY_TEXT[alert.severity])}
          >
            {alert.severity}
          </span>
          <ObservedText
            text={describeAlert(alert).headline}
            className="text-sm text-ink"
            as="span"
          />
        </div>
      ))}

      <section className="space-y-2">
        <SectionLabel>What happened, and what would have happened without it</SectionLabel>
        <IncidentGraph data={data} />
        <p className="px-1 text-xs text-ink-faint">
          The top row is what the assistant did. The bottom row is the same request with that one
          memory cut out of the prompt. Drag to pan, scroll to zoom.
        </p>
      </section>

      <section className="space-y-2">
        <SectionLabel>What the assistant did</SectionLabel>
        <Panel>
          <PanelHeader
            title={
              <span className="font-mono text-sm">
                {decision.tool ? `${decision.tool}()` : "final answer"}
              </span>
            }
            subtitle={
              decision.side_effect
                ? "This action changes something outside the assistant. It cannot be taken back."
                : "This action only read or answered. Nothing outside the assistant changed."
            }
            aside={<TrustBadge trust={decision.trust_min} />}
          />
          <PanelBody className="space-y-4">
            <dl className="grid gap-4 sm:grid-cols-3">
              <Field label="In the conversation">
                <Link
                  to="/runs/$runId"
                  params={{ runId: decision.run_id }}
                  className="text-xs text-accent hover:underline"
                >
                  See the whole thing
                </Link>
              </Field>
              <Field label="When">
                <span className="text-xs text-ink-muted">{formatTime(decision.occurred_at)}</span>
              </Field>
              <Field label="Decided by">
                <span className="font-mono text-xs text-ink-muted">
                  {data.llm_call?.model || "no model recorded"}
                </span>
              </Field>
            </dl>

            {decision.arg_leaves.length === 0 ? (
              <p className="text-sm text-ink-muted">It was given nothing to work with.</p>
            ) : (
              <dl className="divide-y divide-line-soft rounded-md border border-line-soft">
                {decision.arg_leaves.map((leaf) => (
                  <div
                    key={leaf.path}
                    className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2"
                  >
                    <dt className="w-16 shrink-0 font-mono text-xs text-ink-faint">{leaf.path}</dt>
                    <dd className="min-w-0 flex-1 font-mono text-xs text-ink">
                      <ArgValue value={leaf.value} />
                    </dd>
                  </div>
                ))}
              </dl>
            )}
            {hasPlaceholder(decision.arg_leaves) ? (
              <p className="text-xs text-ink-faint">
                Addresses, numbers and keys are hidden before anything is stored. The same value
                always gets the same code, which is how you can watch one move from a page into a
                memory and out into an action.
              </p>
            ) : null}
          </PanelBody>
        </Panel>
      </section>

      {strongest?.taint_path?.length ? (
        <section className="space-y-2">
          <SectionLabel>Where that value came from</SectionLabel>
          <p className="max-w-2xl text-sm text-ink-muted">
            Read left to right. Each step is something Totem recorded as it happened, not a guess
            made afterwards.
          </p>
          <div className="overflow-x-auto pb-1">
            <TaintPath
              decisionLabel={decision.tool ? `${decision.tool}()` : "final answer"}
              nodes={strongest.taint_path}
            />
          </div>
        </section>
      ) : null}

      <section className="space-y-2">
        <SectionLabel>What was in front of it</SectionLabel>
        <p className="max-w-2xl text-sm text-ink-muted">
          Totem keeps four separate claims apart, from the weakest to the strongest. That a memory
          was read is not evidence that it mattered. Only the last claim is, and only a replay can
          make it.
        </p>
        <Panel>
          {data.memories.length === 0 && data.sources.length === 0 ? (
            <Empty>Nothing the assistant remembered reached this action.</Empty>
          ) : (
            <ul className="divide-y divide-line-soft">
              {[...data.memories, ...data.sources].map((influence) => (
                <li key={influence.id} className="px-4 py-3">
                  <InfluenceRow influence={influence} />
                </li>
              ))}
            </ul>
          )}
        </Panel>
      </section>

      {strongest ? (
        <section className="space-y-2">
          <SectionLabel>Proof</SectionLabel>
          <p className="max-w-2xl text-sm text-ink-muted">
            The only way to know whether something caused a decision is to take it away and ask
            again. Totem re-sends the same request with it cut out, several times on each side, and
            compares what comes back.
          </p>
          <ReplayCard
            replay={data.replays.find((r) => r.target_memory_version_id === strongest.id)}
            decisionId={decision.id}
            memoryVersionId={strongest.id}
            memoryLabel={shortKey(strongest.memory_key ?? "this memory")}
            demoMode={data.demo_mode}
          />
        </section>
      ) : null}
    </div>
  );
}

/** Whether any argument value is a redaction placeholder worth explaining. */
function hasPlaceholder(leaves: { path: string; value: string }[]): boolean {
  return leaves.some((leaf) => /\[(EMAIL|PHONE|CARD|KEY|JWT|TOKEN):[0-9a-f]{6}\]/.test(leaf.value));
}

function ArgValue({ value }: { value: string }) {
  const match = PLACEHOLDER_ONLY.exec(value);
  if (match) return <PlaceholderChip kind={match[1]!} digest={match[2]!} />;
  return <ObservedText text={value} as="span" />;
}

function InfluenceRow({ influence }: { influence: Influence }) {
  const title =
    influence.kind === "memory"
      ? shortKey(influence.memory_key ?? "memory")
      : influence.origin || SOURCE_KINDS[influence.source_kind ?? ""] || "something it read";
  // A key or a URL is an identifier and reads as one. A phrase like "what you
  // said" is prose, and setting it in monospace makes it look like a variable.
  const identifier = influence.kind === "memory" || Boolean(influence.origin);

  const link =
    influence.kind === "memory" && influence.memory_id
      ? ({ to: "/memories/$memoryId", params: { memoryId: influence.memory_id } } as const)
      : influence.kind === "source"
        ? ({ to: "/sources/$sourceId", params: { sourceId: influence.id } } as const)
        : undefined;

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        {link ? (
          <Link
            {...link}
            className={cn("text-xs text-accent hover:underline", identifier && "font-mono")}
          >
            {title}
          </Link>
        ) : (
          <span className={cn("text-xs text-ink", identifier && "font-mono")}>{title}</span>
        )}
        <TrustBadge trust={influence.trust_min} reason={influence.trust_reason} />
      </div>

      <ObservedText
        text={influence.content}
        className={cn(
          "rounded-md border-l-2 bg-card px-3 py-2 text-xs text-ink-muted",
          tierBorder(influence.trust_min).replace("/35", "/60"),
        )}
      />

      {/* Each level is a separate claim, and only the last is proof. */}
      <ul className="flex flex-wrap items-center gap-x-4 gap-y-1">
        {influence.labels.map((label, index) => {
          const strongest = index === influence.labels.length - 1;
          return (
            <li
              key={label}
              className={cn(
                "flex items-center gap-1.5 text-xs",
                strongest ? "text-ink" : "text-ink-faint",
              )}
              title="Totem records each level of evidence separately: that it was read, that it reached the prompt, that it supplied a value, and that removing it changed the outcome."
            >
              <Check
                className={cn("size-3", strongest ? "text-trust-3" : "text-ink-faint")}
                aria-hidden="true"
              />
              {label}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
