import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowRight } from "lucide-react";

import { AlertList } from "@/components/AlertList";
import { Dot, ErrorNote, Panel, PanelHeader, SectionLabel, Spinner } from "@/components/ui";
import { api } from "@/lib/api";
import { describeAlert } from "@/lib/describe";
import { TIER_MEANINGS, TIER_WORDS, type Tier, tierTone } from "@/lib/trust";

export function Overview() {
  const { data, isPending, error } = useQuery({ queryKey: ["overview"], queryFn: api.overview });

  if (isPending) return <Spinner label="Loading…" />;
  if (error) {
    return <ErrorNote title="The API could not be reached." detail={(error as Error).message} />;
  }

  if (data.runs === 0) return <NothingYet />;

  const featured = data.featured;
  const featuredAlert = featured ? data.alerts.find((a) => a.id === featured.alert_id) : undefined;
  const copy = featuredAlert ? describeAlert(featuredAlert) : null;
  // The banner is the featured alert in full, so repeating it in the list
  // below would be the same two sentences twice on one screen.
  const rest = featuredAlert ? data.alerts.filter((a) => a.id !== featuredAlert.id) : data.alerts;

  return (
    <div className="mx-auto max-w-5xl space-y-8">
      <Preamble />

      {featured && copy ? (
        <Panel className="bg-trust-0/[0.05]">
          <div className="flex flex-wrap items-center justify-between gap-4 px-4 py-4">
            <div className="min-w-0">
              <p className="flex items-center gap-1.5 text-xs font-medium text-trust-0">
                <Dot tone="trust0" />
                Needs attention
              </p>
              <h1 className="mt-1.5 text-base font-medium text-ink">{copy.headline}</h1>
              <p className="mt-1 max-w-xl text-sm text-ink-muted">{copy.detail}</p>
            </div>
            <Link
              to="/decisions/$decisionId"
              params={{ decisionId: featured.decision_id }}
              className="inline-flex h-8 shrink-0 items-center gap-2 rounded-md bg-primary px-3 text-sm font-medium text-primary-ink transition-[filter] hover:brightness-110"
            >
              {/* The public demo is one known incident and says so. Anywhere
                  else this is somebody's own assistant, and naming their alert
                  a poisoning would be presumptuous. */}
              {data.demo_mode ? "See how this happened" : "See what caused this"}
              <ArrowRight className="size-3.5" aria-hidden="true" />
            </Link>
          </div>
        </Panel>
      ) : null}

      {rest.length > 0 || !featuredAlert ? (
        <section className="space-y-2">
          <SectionLabel>Anything wrong?</SectionLabel>
          <Panel>
            <PanelHeader
              title={featuredAlert ? "Also worth knowing" : "What Totem noticed"}
              subtitle="Newest first. Each one opens the thing it is about."
            />
            <AlertList alerts={rest} />
          </Panel>
        </section>
      ) : null}

      <section className="space-y-2">
        <SectionLabel>What has been recorded</SectionLabel>
        <Panel>
          <dl className="grid grid-cols-3 divide-x divide-line-soft">
            <Stat label="Conversations" value={data.runs} />
            <Stat label="Things it did" value={data.decisions} />
            <Stat label="Things it remembers" value={data.memories} />
          </dl>
        </Panel>
      </section>

      <Legend />
    </div>
  );
}

/** What this application is, for someone who has never seen it. */
function Preamble() {
  return (
    <section className="space-y-2 border-b border-line-soft pb-6">
      <SectionLabel>What you are looking at</SectionLabel>
      <p className="max-w-2xl text-sm leading-relaxed text-ink-muted">
        An AI assistant reads things, writes things down, and acts on what it remembers. When it
        does something you did not expect, the cause is usually buried in something it read days
        ago. <span className="text-ink">Totem records where every action came from</span>, so you
        can follow one back to whatever caused it, and prove the link by re-running the decision
        without it.
      </p>
    </section>
  );
}

/** The colour vocabulary, taught once, where a newcomer will meet it. */
function Legend() {
  const tiers: Tier[] = [3, 2, 1, 0];
  return (
    <section className="space-y-2">
      <SectionLabel>How to read the colours</SectionLabel>
      <Panel className="divide-y divide-line-soft">
        {tiers.map((tier) => (
          <div key={tier} className="flex items-baseline gap-3 px-4 py-2.5">
            <span className="flex w-36 shrink-0 items-center gap-2 text-sm text-ink">
              <Dot tone={tierTone(tier)} />
              {TIER_WORDS[tier]}
            </span>
            <span className="text-xs text-ink-muted">{TIER_MEANINGS[tier]}</span>
          </div>
        ))}
      </Panel>
      <p className="text-xs text-ink-faint">
        Totem trusts what you say and distrusts what the assistant picked up elsewhere. An alert is
        what happens when the second kind reaches an action.
      </p>
    </section>
  );
}

/** The first screen of a fresh clone: no agent has reported anything yet. */
function NothingYet() {
  return (
    <div className="mx-auto max-w-2xl space-y-6 pt-10">
      <div className="space-y-2">
        <SectionLabel>Waiting</SectionLabel>
        <h1 className="text-base font-medium text-ink">Nothing has been recorded yet.</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          An AI assistant reads things, writes things down, and acts on what it remembers. Totem
          records where every action came from. Talk to the assistant and this page fills in as you
          go.
        </p>
      </div>

      <Panel>
        <PanelHeader
          title="Start a conversation"
          subtitle="Brings up the database, the server and this page, then hands you a prompt."
        />
        <div className="border-t border-line-soft px-4 py-3">
          <code className="font-mono text-sm text-ink">make start</code>
        </div>
      </Panel>

      <Panel>
        <PanelHeader
          title="Or watch the staged attack"
          subtitle="An assistant reads a poisoned page, remembers what it says, and later emails the attacker. No API key needed."
        />
        <div className="border-t border-line-soft px-4 py-3">
          <code className="font-mono text-sm text-ink">TM_DEMO_FAKE_MODEL=true make demo</code>
        </div>
      </Panel>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="px-4 py-3">
      <dt className="text-xs text-ink-faint">{label}</dt>
      <dd className="mt-0.5 text-lg text-ink tabular-nums">{value}</dd>
    </div>
  );
}
