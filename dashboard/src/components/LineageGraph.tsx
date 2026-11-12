import {
  Background,
  BackgroundVariant,
  Controls,
  type Edge,
  type Node,
  Position,
  ReactFlow,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useMemo } from "react";

import type { MemoryPage } from "@/lib/api";
import { tierWords } from "@/lib/trust";
import { tierOf } from "@/lib/trust";

const TIER_COLOURS: Record<number, string> = {
  0: "var(--color-trust-0)",
  1: "var(--color-trust-1)",
  2: "var(--color-trust-2)",
  3: "var(--color-trust-3)",
  4: "var(--color-trust-4)",
};

const PANEL = "var(--color-card)";

/**
 * A memory's provenance as a graph. Versions sit on the left and what they
 * derive from on the right, so an arrow reads "this version derived from
 * that", the same direction as the path on the decision page.
 */
export function LineageGraph({ graph }: { graph: MemoryPage["graph"] }) {
  const { nodes, edges } = useMemo(() => layout(graph), [graph]);

  return (
    <div className="h-[22rem] w-full overflow-hidden rounded-md border border-line-soft bg-bg">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        fitView
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        edgesFocusable={false}
        colorMode="dark"
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  );
}

function layout(graph: MemoryPage["graph"]): { nodes: Node[]; edges: Edge[] } {
  // Sources on the left, memory versions on the right, ordered by version.
  const sources = graph.nodes.filter((n) => n.kind === "source");
  const versions = [...graph.nodes.filter((n) => n.kind === "memory_version")].sort(
    (a, b) => (a.version ?? 0) - (b.version ?? 0),
  );

  const nodes: Node[] = [
    // Arrows leave the right edge and arrive at the left one, so the graph
    // reads across the page. With the default top and bottom handles the
    // lines loop around the boxes and cross each other.
    ...versions.map((node, index) => ({
      id: node.id,
      position: { x: 0, y: index * 130 },
      data: { label: nodeLabel(node.label, node.trust_min) },
      style: nodeStyle(node.trust_min),
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      type: "input" as const,
    })),
    ...sources.map((node, index) => ({
      id: node.id,
      position: { x: 360, y: index * 130 },
      data: { label: nodeLabel(node.label, node.trust_min) },
      style: nodeStyle(node.trust_min),
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
      type: "output" as const,
    })),
  ];

  const edges: Edge[] = graph.edges.map((edge) => ({
    id: edge.id,
    source: edge.from,
    target: edge.to,
    label: edge.kind === "DERIVED_FROM" ? "derived from" : "supersedes",
    // Not animated. A dashed line that crawls forever reads as something
    // still happening, and this is a finished record of what already did.
    animated: false,
    type: "smoothstep",
    style: { stroke: "var(--color-line)" },
    labelStyle: { fill: "var(--color-ink-faint)", fontSize: 10 },
    labelBgStyle: { fill: "var(--color-bg)" },
  }));

  return { nodes, edges };
}

/**
 * A node says what it is and where it came from. The memory label arrives as
 * "memory <namespaced key> v1"; the namespace is the same on every node here,
 * so it is dropped and the version kept.
 */
function nodeLabel(label: string, trust: number): string {
  const memory = /^memory (\S+) (v\d+)$/.exec(label);
  const name = memory ? `${memory[1]!.slice(memory[1]!.lastIndexOf("/") + 1)} ${memory[2]}` : label;
  return `${name}\n${tierWords(trust)}`;
}

function nodeStyle(trust: number): React.CSSProperties {
  return {
    background: PANEL,
    border: "1px solid var(--color-line)",
    borderLeft: `2px solid ${TIER_COLOURS[tierOf(trust)]}`,
    borderRadius: 6,
    color: "var(--color-ink)",
    fontFamily: "var(--font-mono)",
    fontSize: 10,
    lineHeight: 1.5,
    padding: "8px 10px",
    width: 230,
    whiteSpace: "pre-line",
    textAlign: "left",
  };
}
