import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { IntervalBar } from "@/components/IntervalBar";
import { ObservedText } from "@/components/ObservedText";
import { Button, Dot, Panel, PanelBody, PanelHeader } from "@/components/ui";
import { api, type Replay } from "@/lib/api";
import { cn } from "@/lib/cn";
import { formatEffect } from "@/lib/format";

const VERDICT_TEXT: Record<string, string> = {
  strong: "text-trust-3",
  moderate: "text-trust-2",
  none: "text-ink-muted",
  inconclusive: "text-ink-muted",
};

const VERDICT_TONE: Record<string, "trust3" | "trust2" | "muted"> = {
  strong: "trust3",
  moderate: "trust2",
  none: "muted",
  inconclusive: "muted",
};

const VERDICT_MEANINGS: Record<string, string> = {
  strong: "Removing this memory changed the decision, clearly and repeatably.",
  moderate: "Removing this memory changed the decision some of the time.",
  none: "Removing this memory made no difference.",
  inconclusive: "The samples cannot tell either way. More samples would help.",
};

/**
 * The proof. A replay re-runs the recorded model call with one memory cut out
 * of the prompt and reports what the agent decided instead. It never runs the
 * agent's tools: it only asks the model.
 */
export function ReplayCard({
  replay,
  decisionId,
  memoryVersionId,
  memoryLabel,
  demoMode,
}: {
  replay?: Replay;
  decisionId: string;
  memoryVersionId: string;
  memoryLabel: string;
  demoMode: boolean;
}) {
  const [startedId, setStartedId] = useState<string | null>(null);

  const live = useQuery({
    queryKey: ["replay", startedId],
    queryFn: () => api.replay(startedId!),
    enabled: startedId !== null,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "done" || status === "failed" ? false : 1500;
    },
  });

  const start = useMutation({
    mutationFn: () =>
      api.startReplay({
        decision_id: decisionId,
        memory_version_id: memoryVersionId,
        samples_per_arm: 0,
      }),
    onSuccess: (result) => setStartedId(result.id),
  });

  const current = live.data ?? replay;

  if (!current) {
    return (
      <Panel>
        <PanelHeader
          title="Replay"
          subtitle={`Ask the model the same thing again with ${memoryLabel} taken out, several times each way, and see whether it still decides the same.`}
        />
        <PanelBody className="space-y-2">
          {demoMode ? (
            <p className="text-sm text-ink-muted">
              This is a recorded run on a read-only demo, so no new replay can be started here.
            </p>
          ) : (
            <>
              <Button onClick={() => start.mutate()} disabled={start.isPending}>
                {start.isPending ? "Starting…" : "Replay without this memory"}
              </Button>
              {start.error ? (
                <p className="observed rounded-md border-l-2 border-trust-0/60 bg-card px-3 py-2 text-xs text-ink-muted">
                  {(start.error as Error).message}
                </p>
              ) : null}
            </>
          )}
        </PanelBody>
      </Panel>
    );
  }

  const running = current.status === "pending" || current.status === "running";
  const verdict = current.verdict ?? "inconclusive";
  const proven = verdict === "strong" || verdict === "moderate";

  return (
    <Panel data-testid="replay-card">
      <PanelHeader
        title="Replay"
        subtitle={`${current.samples_per_arm} tries each way${
          current.model ? ` on ${current.model}` : ""
        }. This only asks the model what it would do. It never runs the assistant's tools, so nothing happens twice.`}
        aside={
          current.status === "done" ? (
            <span className="flex items-center gap-1.5 text-xs">
              <Dot tone={VERDICT_TONE[verdict] ?? "muted"} />
              <span className={cn("font-medium", VERDICT_TEXT[verdict])}>
                {verdict}
              </span>
            </span>
          ) : (
            <span className="text-xs text-ink-faint">
              {current.status}
            </span>
          )
        }
      />
      <PanelBody className="space-y-4">
        <p className="text-sm text-ink" data-testid="replay-sentence">
          {current.sentence}
        </p>

        {current.status === "failed" ? (
          <p className="observed rounded-md border-l-2 border-trust-0/60 bg-card px-3 py-2 text-xs text-ink-muted">
            {current.error}
          </p>
        ) : null}

        {current.status === "done" && current.effect !== null ? (
          <div className="space-y-3">
            <IntervalBar
              effect={current.effect}
              low={current.ci_low ?? current.effect}
              high={current.ci_high ?? current.effect}
              tone={proven ? "proven" : "unproven"}
            />
            <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Stat
                label="Difference"
                value={formatEffect(current.effect)}
                hint="How much more often the decision changed when the memory was taken out."
              />
              <Stat
                label="Could be as much as"
                value={`${formatEffect(current.ci_low)} to ${formatEffect(current.ci_high)}`}
                hint="The range the real difference is almost certainly inside, given this many tries. A range that stays above zero is what makes the result a proof rather than a hunch."
              />
              <Stat
                label="Changed its mind"
                value={`${current.changed} of ${current.total}`}
                hint="How many tries without the memory came out differently."
              />
              <Stat
                label="Varies on its own"
                value={formatEffect(current.baseline_flip)}
                hint="How often the model changed its answer even with nothing removed. A model that wanders on its own cannot prove anything."
              />
            </dl>
            <p className="text-sm text-ink-muted">{VERDICT_MEANINGS[verdict]}</p>
          </div>
        ) : null}

        {running ? (
          <p className="text-sm text-ink-muted" role="status">
            Sampling the model…
          </p>
        ) : null}

        <div className="grid gap-3 md:grid-cols-2">
          <Arm
            title="With the memory"
            note="Exactly what the assistant was asked at the time."
            samples={current.baseline}
          />
          <Arm
            title="Without the memory"
            note={`The same thing, with ${memoryLabel} cut out.`}
            samples={current.variant}
          />
        </div>
      </PanelBody>
    </Panel>
  );
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div>
      <dt className="text-xs text-ink-faint" title={hint}>
        {label}
      </dt>
      <dd className="mt-0.5 font-mono text-sm text-ink tabular-nums">{value}</dd>
    </div>
  );
}

function Arm({
  title,
  note,
  samples,
}: {
  title: string;
  note: string;
  samples: Replay["baseline"];
}) {
  return (
    <div className="rounded-md border border-line-soft">
      <div className="border-b border-line-soft px-3 py-2">
        <h3 className="text-xs font-medium text-ink">{title}</h3>
        <p className="mt-0.5 text-xs text-ink-faint">{note}</p>
      </div>
      {samples.length === 0 ? (
        <p className="px-3 py-3 text-xs text-ink-faint">No samples yet.</p>
      ) : (
        <ol className="divide-y divide-line-soft">
          {samples.map((sample) => (
            <li key={sample.idx} className="flex items-start gap-2 px-3 py-1.5">
              <Dot tone={sample.matches_baseline ? "muted" : "trust3"} className="mt-[0.4rem]" />
              <ObservedText
                text={sample.summary || "(no tool call)"}
                className="min-w-0 font-mono text-xs text-ink-muted"
                as="span"
              />
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
