import { useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { lazy, Suspense } from "react";

import { AlertList } from "@/components/AlertList";
import { ObservedText } from "@/components/ObservedText";
import { TrustBadge } from "@/components/TrustBadge";
import {
  Dot,
  ErrorNote,
  Panel,
  PanelBody,
  PanelHeader,
  SectionLabel,
  Spinner,
} from "@/components/ui";
import { api } from "@/lib/api";
import { cn } from "@/lib/cn";
import { shortKey } from "@/lib/describe";
import { formatTime, plural } from "@/lib/format";
import { tierBorder } from "@/lib/trust";

// The graph library is a third of the bundle and only this page uses it, so
// the rest of the dashboard does not pay for it.
const LineageGraph = lazy(async () => ({
  default: (await import("@/components/LineageGraph")).LineageGraph,
}));

/** What a lineage precision means, for the tooltip. */
const PRECISION: Record<string, string> = {
  step: "it was attributed to the handful of things the model had actually read at that step.",
  run: "it was attributed to everything the agent had seen in that run.",
};

export function MemoryPage() {
  const { memoryId } = useParams({ from: "/memories/$memoryId" });
  const { data, isPending, error } = useQuery({
    queryKey: ["memory", memoryId],
    queryFn: () => api.memory(memoryId),
  });

  if (isPending) return <Spinner label="Loading the memory…" />;
  if (error) {
    return <ErrorNote title="This memory could not be loaded." detail={(error as Error).message} />;
  }

  const versions = [...data.versions].reverse();

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <section className="space-y-2">
        <SectionLabel>Memory</SectionLabel>
        <Panel>
          <PanelHeader
            title={<span className="font-mono text-sm">{shortKey(data.memory.memory_key)}</span>}
            subtitle={`${data.memory.memory_key} · ${data.memory.status} · ${plural(
              data.versions.length,
              "version",
              "versions",
            )}`}
          />
          <PanelBody className="space-y-3">
            <p className="max-w-2xl text-sm text-ink-muted">
              What the assistant wrote down, and what it was looking at when it did. Follow the
              arrows right to see where the content came from.
            </p>
            <Suspense fallback={<Spinner label="Drawing…" />}>
              <LineageGraph graph={data.graph} />
            </Suspense>
          </PanelBody>
        </Panel>
      </section>

      {data.alerts.length > 0 ? (
        <section className="space-y-2">
          <SectionLabel>Alerts on this memory</SectionLabel>
          <Panel>
            <AlertList alerts={data.alerts} hereId={data.memory.id} />
          </Panel>
        </section>
      ) : null}

      <section className="space-y-2">
        <SectionLabel
          aside={
            <span className="text-xs text-ink-faint">
              a write that changed nothing is not a version
            </span>
          }
        >
          Versions
        </SectionLabel>
        <Panel>
          <ul className="divide-y divide-line-soft">
            {versions.map((version) => (
              <li key={version.id} className="px-4 py-3">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span className="font-mono text-xs text-ink">v{version.version}</span>
                  <TrustBadge trust={version.trust_min} />
                  {version.deleted ? (
                    <span className="flex items-center gap-1.5 text-xs text-ink-faint">
                      <Dot tone="muted" />
                      deleted
                    </span>
                  ) : null}
                  {version.superseded_at === null && !version.deleted ? (
                    <span className="flex items-center gap-1.5 text-xs text-accent">
                      <Dot tone="accent" />
                      current
                    </span>
                  ) : null}
                  <span
                    className="text-xs text-ink-faint"
                    title={`Lineage was attributed at ${version.lineage_precision} precision: ${PRECISION[version.lineage_precision] ?? "how exactly Totem could tell what this was derived from."}`}
                  >
                    written {formatTime(version.recorded_at)}
                  </span>
                </div>
                {version.content ? (
                  <ObservedText
                    text={version.content}
                    className={cn(
                      "mt-2 rounded-md border-l-2 bg-card px-3 py-2 text-xs text-ink-muted",
                      tierBorder(version.trust_min).replace("/35", "/60"),
                    )}
                  />
                ) : null}
              </li>
            ))}
          </ul>
        </Panel>
      </section>
    </div>
  );
}
