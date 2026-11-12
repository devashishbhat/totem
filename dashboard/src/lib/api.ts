/**
 * The read model, as totem-server serves it.
 *
 * Every string in here is text the agent observed. It is rendered as plain
 * text, never as markup: see `ObservedText`.
 */

export type AttributionLevel = "retrieved" | "in_context" | "value_flow" | "counterfactual";

export type AlertTarget = { kind: "decision" | "memory" | "source"; id: string };

export interface Alert {
  id: string;
  kind: string;
  severity: "critical" | "high" | "medium" | "low";
  subject_id: string;
  run_id: string;
  title: string;
  evidence: Record<string, unknown>;
  created_at: string;
  target: AlertTarget;
}

export interface Overview {
  demo_mode: boolean;
  runs: number;
  decisions: number;
  memories: number;
  alerts: Alert[];
  featured: {
    alert_id: string;
    decision_id: string;
    title: string;
    severity: string;
  } | null;
}

export interface RunSummary {
  id: string;
  agent_id: string;
  scope: string;
  started_at: string;
  decisions: number;
  alerts: number;
  trust_min: number;
  tier: string;
  /** The first thing the user said, which is how a person recognises a run. */
  opening: string;
}

export interface TimelineItem {
  kind: "source" | "memory.read" | "llm.call" | "decision" | "memory.write";
  id: string;
  at: string;
  title: string;
  text?: string;
  trust_min?: number;
  tier?: string;
  link?: AlertTarget;
  meta?: Record<string, unknown>;
}

export interface Run {
  id: string;
  agent_id: string;
  scope: string;
  started_at: string;
}

export interface Timeline {
  run: Run;
  items: TimelineItem[];
  opening: string;
}

export interface PathNode {
  kind: "memory_version" | "source";
  id: string;
  label: string;
  text?: string;
  trust_min: number;
  tier: string;
  origin?: string;
  memory_id?: string;
  memory_key?: string;
  version?: number;
  reason?: string;
}

export interface ReplaySample {
  idx: number;
  output: unknown;
  matches_baseline: boolean;
  summary: string;
}

export interface Replay {
  id: string;
  decision_id: string;
  target_memory_version_id: string;
  samples_per_arm: number;
  model: string;
  baseline_flip: number | null;
  variant_flip: number | null;
  effect: number | null;
  ci_low: number | null;
  ci_high: number | null;
  verdict: "strong" | "moderate" | "none" | "inconclusive" | null;
  status: "pending" | "running" | "done" | "failed";
  error?: string;
  created_at: string;
  finished_at: string | null;
  changed: number;
  total: number;
  baseline: ReplaySample[];
  variant: ReplaySample[];
  sentence: string;
  target?: {
    memory_version_id: string;
    memory_id: string;
    memory_key: string;
    version: number;
  };
}

export interface Influence {
  kind: "memory" | "source";
  id: string;
  memory_id?: string;
  memory_key?: string;
  version?: number;
  origin?: string;
  source_kind?: string;
  content: string;
  trust_min: number;
  tier: string;
  trust_reason?: string;
  level: AttributionLevel;
  levels: AttributionLevel[];
  labels: string[];
  arg_paths?: string[];
  spans?: [number, number, number][];
  taint_path?: PathNode[];
  replay?: Replay;
}

export interface Decision {
  id: string;
  run_id: string;
  llm_call_id: string | null;
  tool: string;
  side_effect: boolean;
  args: Record<string, unknown>;
  trust_min: number;
  taint_source_ids: string[];
  occurred_at: string;
  tier: string;
  arg_leaves: { path: string; value: string }[];
}

export interface Explain {
  decision: Decision;
  run: Run;
  llm_call: { id: string; model: string; occurred_at: string } | null;
  alerts: Alert[];
  memories: Influence[];
  sources: Influence[];
  replays: Replay[];
  demo_mode: boolean;
}

export interface MemoryVersion {
  id: string;
  memory_id: string;
  memory_key?: string;
  version: number;
  hmac: string;
  content: string;
  deleted: boolean;
  lineage_precision: string;
  trust_min: number;
  taint_source_ids: string[];
  recorded_at: string;
  superseded_at: string | null;
  tier: string;
}

export interface MemoryPage {
  memory: { id: string; memory_key: string; status: string; current_version: number };
  versions: MemoryVersion[];
  graph: {
    nodes: PathNode[];
    edges: { id: string; kind: string; from: string; to: string }[];
  };
  alerts: Alert[];
}

export interface SourcePage {
  source: {
    id: string;
    run_id: string;
    kind: string;
    origin: string;
    hmac: string;
    content: string;
    trust: number;
    trust_reason: string;
    observed_at: string;
  };
  tier: string;
}

/** Where the API lives. Set at build time by VITE_API_URL. */
export const API_URL: string = (import.meta.env.VITE_API_URL ?? "http://localhost:8080").replace(
  /\/$/,
  "",
);

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_URL}/api/v1${path}`, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    try {
      const body = (await response.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // The body was not JSON; the status is all we know.
    }
    throw new ApiError(response.status, message);
  }
  return withLists((await response.json()) as unknown) as T;
}

/**
 * Field names this API declares as arrays.
 *
 * A Go nil slice marshals to JSON null, so an endpoint that means "no alerts"
 * can send `null` where the type says `Alert[]`. The server is written to send
 * `[]` and has a test saying so, but one missed initialiser took out a whole
 * page with "Cannot read properties of null (reading 'map')". This keeps a
 * missing list from ever being worse than an empty one.
 *
 * Only names that are always arrays belong here. Fields that are genuinely
 * nullable, such as `llm_call` or `verdict`, must keep their null.
 */
const LIST_FIELDS = new Set([
  "alerts",
  "arg_leaves",
  "arg_paths",
  "baseline",
  "edges",
  "items",
  "labels",
  "levels",
  "memories",
  "nodes",
  "replays",
  "runs",
  "sources",
  "taint_path",
  "taint_source_ids",
  "variant",
  "versions",
]);

function withLists(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(withLists);
  if (value === null || typeof value !== "object") return value;
  const out: Record<string, unknown> = {};
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    out[key] = child === null && LIST_FIELDS.has(key) ? [] : withLists(child);
  }
  return out;
}

export const api = {
  overview: () => request<Overview>("/overview"),
  runs: () => request<{ runs: RunSummary[] }>("/runs"),
  timeline: (runId: string) => request<Timeline>(`/runs/${encodeURIComponent(runId)}/timeline`),
  explain: (decisionId: string) => request<Explain>(`/decisions/${decisionId}/explain`),
  memory: (memoryId: string) => request<MemoryPage>(`/memories/${memoryId}`),
  source: (sourceId: string) => request<SourcePage>(`/sources/${sourceId}`),
  replay: (replayId: string) => request<Replay>(`/replays/${replayId}`),
  startReplay: (body: {
    decision_id: string;
    memory_version_id: string;
    samples_per_arm: number;
  }) => request<{ id: string }>("/replays", { method: "POST", body: JSON.stringify(body) }),
};
