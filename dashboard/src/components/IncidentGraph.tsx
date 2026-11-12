import {
  Background,
  BackgroundVariant,
  type Edge,
  type Node,
  Position,
  ReactFlow,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useMemo } from "react";

import type { Explain, Influence, Replay } from "@/lib/api";
import { plainPlaceholders, shortKey } from "@/lib/describe";
import { tierOf, tierWords } from "@/lib/trust";

/**
 * One incident, as two rows that can be compared at a glance.
 *
 * The top row is what the assistant actually did, read left to right: what
 * you asked, what it read, what it wrote down, what it then did. The bottom
 * row is the same question asked again with that one memory cut out of the
 * prompt, which is the only evidence that the memory caused anything.
 *
 * Reading two rows against each other is the whole product in one picture,
 * so this is the first thing on the page rather than the last.
 */

const COLOURS: Record<number, string> = {
  0: "var(--color-trust-0)",
  1: "var(--color-trust-1)",
  2: "var(--color-trust-2)",
  3: "var(--color-trust-3)",
  4: "var(--color-trust-4)",
};

const WIDTH = 236;
const GAP = 288;
const LANE_A = 56;
const LANE_B = 232;

type Step = {
  id: string;
  title: string;
  detail: string;
  foot: string;
  /** The colour of the left edge: a trust tier, or an outcome. */
  accent: string;
  dim?: boolean;
};

export function IncidentGraph({ data }: { data: Explain }) {
  const { nodes, edges } = useMemo(() => build(data), [data]);

  return (
    <div className="incident-graph h-[26rem] w-full overflow-hidden rounded-lg border border-line-soft bg-bg">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        fitView
        fitViewOptions={{ padding: 0.12 }}
        minZoom={0.3}
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        edgesFocusable={false}
        nodesFocusable={false}
        colorMode="dark"
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
      </ReactFlow>
    </div>
  );
}

/** The memory the replay removed, or the strongest one if there was no replay. */
function culprit(data: Explain): Influence | undefined {
  return (
    data.memories.find((m) => m.level === "counterfactual") ??
    data.memories.find((m) => m.level === "value_flow") ??
    data.memories[0]
  );
}

function argSummary(args: Record<string, unknown>, paths: string[] | undefined): string {
  const path = paths?.[0];
  if (path && typeof args[path] === "string") return `${path}: ${args[path] as string}`;
  const first = Object.entries(args).find(([, v]) => typeof v === "string");
  return first ? `${first[0]}: ${first[1] as string}` : "";
}

function build(data: Explain): { nodes: Node[]; edges: Edge[] } {
  const memory = culprit(data);
  const replay = data.replays.find((r) => r.status === "done" && r.verdict);
  const tool = data.decision.tool || "answered";

  // --- the top row: what actually happened -------------------------------
  const asked = data.sources.find((s) => s.source_kind === "user_message");
  const read = memory?.taint_path?.find((p) => p.kind === "source");

  const top: Step[] = [];
  if (asked) {
    top.push({
      id: "asked",
      title: "You asked",
      detail: clip(plainPlaceholders(asked.content), 72),
      foot: tierWords(asked.trust_min),
      accent: COLOURS[tierOf(asked.trust_min)]!,
    });
  }
  if (read) {
    top.push({
      id: "read",
      title: "It read a page",
      detail: clip(read.label ?? "", 64),
      foot: tierWords(read.trust_min),
      accent: COLOURS[tierOf(read.trust_min)]!,
    });
  }
  if (memory) {
    top.push({
      id: "memory",
      title: "It wrote that down",
      detail: `${shortKey(memory.memory_key ?? "")} = ${clip(memory.content, 40)}`,
      foot: tierWords(memory.trust_min),
      accent: COLOURS[tierOf(memory.trust_min)]!,
    });
  }
  top.push({
    id: "did",
    title: `It ran ${tool}`,
    detail: clip(argSummary(data.decision.args, memory?.arg_paths), 56),
    foot: "what happened",
    accent: COLOURS[0]!,
  });

  // --- the bottom row: the same question without that memory -------------
  const bottom: Step[] = [];
  if (asked) {
    bottom.push({
      id: "asked-b",
      title: "You asked",
      detail: clip(plainPlaceholders(asked.content), 72),
      foot: "the same thing",
      accent: COLOURS[tierOf(asked.trust_min)]!,
      dim: true,
    });
  }
  bottom.push({
    id: "removed",
    title: "Without that memory",
    detail: memory ? `${shortKey(memory.memory_key ?? "")} cut out of the prompt` : "removed",
    foot: replay ? `${replay.samples_per_arm} tries` : "not measured yet",
    accent: "var(--color-ink-faint)",
    dim: true,
  });
  bottom.push(outcomeStep(replay, tool));

  const nodes: Node[] = [
    label("lane-a", "What happened", LANE_A - 44, COLOURS[0]!),
    label("lane-b", replayCaption(replay), LANE_B - 44, replay ? COLOURS[3]! : undefined),
    ...top.map((s, i) => stepNode(s, i * GAP, LANE_A)),
    ...bottom.map((s, i) => stepNode(s, i * GAP, LANE_B)),
  ];

  const edges: Edge[] = [
    ...chain(top, "a"),
    ...chain(bottom, "b"),
  ];

  return { nodes, edges };
}

/** The last node of the lower row: what the model decided without the memory. */
function outcomeStep(replay: Replay | undefined, tool: string): Step {
  if (!replay) {
    return {
      id: "would",
      title: "Not yet known",
      detail: "Run the replay below to find out what it would have done instead.",
      foot: "no replay yet",
      accent: "var(--color-ink-faint)",
      dim: true,
    };
  }
  // A sample's output is recorded as unknown, because it is whatever the
  // model answered. Read the arguments defensively rather than asserting.
  const sample = replay.variant?.find((s) => s.output);
  const output = (sample?.output ?? {}) as { args?: Record<string, unknown> };
  const args = output.args ?? {};
  const changed = replay.changed === replay.total && replay.total > 0;
  return {
    id: "would",
    title: changed ? `It ran ${tool} differently` : `It ran ${tool} the same way`,
    detail: clip(argSummary(args, undefined), 56),
    foot: changed ? "every time" : "not every time",
    accent: changed ? COLOURS[3]! : COLOURS[2]!,
  };
}

function replayCaption(replay: Replay | undefined): string {
  if (!replay) return "What it would have done";
  return `Without that memory: ${replay.changed} of ${replay.total} came out different`;
}

function chain(steps: Step[], lane: string): Edge[] {
  return steps.slice(1).map((step, i) => ({
    id: `${lane}-${i}`,
    source: steps[i]!.id,
    target: step.id,
    // Never animated: this is a record of what already happened, and a line
    // that crawls forever reads as something still in progress.
    animated: false,
    type: "smoothstep",
    style: { stroke: "var(--color-line)", strokeWidth: 1.5 },
  }));
}

function stepNode(step: Step, x: number, y: number): Node {
  return {
    id: step.id,
    position: { x, y },
    data: { label: `${step.title}\n${step.detail}\n${step.foot}` },
    draggable: false,
    // The row reads left to right, so the line leaves the right edge and
    // arrives at the left one. With the default top and bottom handles each
    // link loops under its own node and the row stops reading as a sequence.
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
    style: {
      background: "var(--color-card)",
      border: "1px solid var(--color-line)",
      borderLeft: `2px solid ${step.accent}`,
      borderRadius: 8,
      color: step.dim ? "var(--color-ink-muted)" : "var(--color-ink)",
      fontFamily: "var(--font-sans)",
      fontSize: 12,
      lineHeight: 1.55,
      padding: "10px 12px",
      width: WIDTH,
      whiteSpace: "pre-line",
      textAlign: "left",
      opacity: step.dim ? 0.78 : 1,
    },
  };
}

function label(id: string, text: string, y: number, accent?: string): Node {
  return {
    id,
    position: { x: 0, y },
    data: { label: text },
    draggable: false,
    selectable: false,
    style: {
      background: "transparent",
      border: "none",
      color: accent ?? "var(--color-ink-faint)",
      fontFamily: "var(--font-sans)",
      fontSize: 12,
      fontWeight: 500,
      padding: 0,
      textAlign: "left",
      width: WIDTH * 2,
    },
  };
}

function clip(text: string, max: number): string {
  const flat = text.replace(/\s+/g, " ").trim();
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
}
