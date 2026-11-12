import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";

import { ObservedText } from "@/components/ObservedText";
import { TrustBadge } from "@/components/TrustBadge";
import {
  ErrorNote,
  Field,
  Panel,
  PanelBody,
  PanelHeader,
  SectionLabel,
  Spinner,
} from "@/components/ui";
import { api } from "@/lib/api";
import { formatTime } from "@/lib/format";

const KIND_LABELS: Record<string, string> = {
  user_message: "Something you said",
  system_config: "The assistant's own instructions",
  tool_output: "What a tool replied",
  external_document: "A page the assistant read",
  unknown: "Something of unknown origin",
};

export function SourcePage() {
  const { sourceId } = useParams({ from: "/sources/$sourceId" });
  const { data, isPending, error } = useQuery({
    queryKey: ["source", sourceId],
    queryFn: () => api.source(sourceId),
  });

  if (isPending) return <Spinner label="Loading…" />;
  if (error) {
    return <ErrorNote title="This could not be loaded." detail={(error as Error).message} />;
  }

  const { source } = data;
  return (
    <div className="mx-auto max-w-4xl space-y-2">
      <SectionLabel>Where this came from</SectionLabel>
      <Panel>
        <PanelHeader
          title={KIND_LABELS[source.kind] ?? source.kind}
          subtitle={<span className="font-mono">{source.origin}</span>}
          aside={<TrustBadge trust={source.trust} reason={source.trust_reason} />}
        />
        <PanelBody className="space-y-4">
          <dl className="grid gap-4 sm:grid-cols-3">
            <Field label="In the conversation">
              <Link
                to="/runs/$runId"
                params={{ runId: source.run_id }}
                className="text-xs text-accent hover:underline"
              >
                See the whole thing
              </Link>
            </Field>
            <Field label="Seen">
              <span className="text-xs text-ink-muted">{formatTime(source.observed_at)}</span>
            </Field>
            <Field label="Why Totem rates it this way">
              <span className="text-xs text-ink-muted">
                {source.trust_reason || "default for its kind"}
              </span>
            </Field>
          </dl>
          <div>
            <p className="text-xs text-ink-faint">What it says</p>
            <ObservedText
              text={source.content}
              className="mt-2 max-h-96 overflow-y-auto rounded-md bg-card px-3 py-2 font-mono text-xs text-ink-muted"
            />
          </div>
        </PanelBody>
      </Panel>
    </div>
  );
}
