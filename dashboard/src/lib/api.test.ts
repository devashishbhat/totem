import { describe, expect, it, vi } from "vitest";

import { api } from "@/lib/api";

/**
 * A Go nil slice marshals to JSON null. Every decision the assistant answered
 * without a tool came back with `"alerts": null`, and the decision page died
 * on `data.alerts.map`. The server sends `[]` now; this is the second line of
 * defence, because the cost of being wrong is a blank page.
 */
describe("the api client", () => {
  function respondWith(body: unknown) {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve(body) }),
    );
  }

  it("turns a null list into an empty one", async () => {
    respondWith({ decision: { id: "d1" }, alerts: null, memories: null, replays: null });
    const explain = await api.explain("d1");
    expect(explain.alerts).toEqual([]);
    expect(explain.memories).toEqual([]);
    expect(explain.replays).toEqual([]);
  });

  it("reaches lists nested inside the response", async () => {
    respondWith({ memory: { id: "m1" }, versions: [{ id: "v1", taint_source_ids: null }] });
    const page = await api.memory("m1");
    expect(page.versions[0]!.taint_source_ids).toEqual([]);
  });

  it("leaves a genuinely nullable field alone", async () => {
    respondWith({ decision: { id: "d1" }, llm_call: null, alerts: [] });
    const explain = await api.explain("d1");
    expect(explain.llm_call).toBeNull();
  });

  it("does not disturb a list that is already a list", async () => {
    respondWith({ runs: [{ id: "run-1" }, { id: "run-2" }] });
    const runs = await api.runs();
    expect(runs.runs.map((r) => r.id)).toEqual(["run-1", "run-2"]);
  });
});
