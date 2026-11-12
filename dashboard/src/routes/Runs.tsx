import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { TrustBadge } from "@/components/TrustBadge";
import { Empty, ErrorNote, Panel, PanelHeader, SectionLabel, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { cn } from "@/lib/cn";
import { plainPlaceholders } from "@/lib/describe";
import { formatTime, plural } from "@/lib/format";

export function Runs() {
  const { data, isPending, error } = useQuery({ queryKey: ["runs"], queryFn: api.runs });

  if (isPending) return <Spinner label="Loading…" />;
  if (error) {
    return (
      <ErrorNote title="The conversations could not be loaded." detail={(error as Error).message} />
    );
  }

  return (
    <div className="mx-auto max-w-5xl space-y-2">
      <SectionLabel>Conversations</SectionLabel>
      <Panel>
        <PanelHeader
          title="Everything the assistant has done, newest first"
          subtitle="The label on the right is the least trusted thing that reached an action in that conversation."
        />
        {data.runs.length === 0 ? (
          <Empty>Nothing yet. Run `make start` and talk to the assistant.</Empty>
        ) : (
          <ul className="divide-y divide-line-soft">
            {data.runs.map((run) => (
              <li key={run.id}>
                <Link
                  to="/runs/$runId"
                  params={{ runId: run.id }}
                  className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 transition-colors hover:bg-card/50"
                >
                  <div className="min-w-0 flex-1">
                    {/* What was said, not the id: that is what a person
                        recognises a conversation by. */}
                    <p
                      className={cn(
                        "truncate text-sm",
                        run.opening ? "text-ink" : "font-mono text-xs text-ink",
                      )}
                    >
                      {run.opening ? plainPlaceholders(run.opening) : run.id.replace(/^run-/, "")}
                    </p>
                    <p className="mt-0.5 text-xs text-ink-faint">
                      {formatTime(run.started_at)}
                      {run.scope ? ` · ${run.scope}` : ""} · {run.agent_id}
                    </p>
                  </div>
                  <span className="text-xs text-ink-muted tabular-nums">
                    {plural(run.decisions, "action", "actions")}
                  </span>
                  {run.alerts > 0 ? (
                    <span className="text-xs text-trust-1 tabular-nums">
                      {run.alerts} alert{run.alerts === 1 ? "" : "s"}
                    </span>
                  ) : null}
                  <TrustBadge trust={run.trust_min} />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}
