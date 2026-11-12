import { describe, expect, it } from "vitest";

import type { Alert } from "@/lib/api";
import { describeAlert, plainPlaceholders, shortKey } from "@/lib/describe";

const base: Alert = {
  id: "a",
  kind: "taint.side_effect_from_untrusted",
  severity: "critical",
  subject_id: "d",
  run_id: "run-1",
  title: "Untrusted memory chose `to` of send_email",
  evidence: {
    tool: "send_email",
    value_flow: [
      {
        arg_path: "to",
        memory_key: "crm/memories/acme.billing_address",
        origin: "https://vendor.example/notice",
      },
    ],
  },
  created_at: new Date().toISOString(),
  target: { kind: "decision", id: "d" },
};

describe("describeAlert", () => {
  it("says what happened without using the vocabulary", () => {
    const copy = describeAlert(base);
    expect(copy.headline).toBe("Something the assistant read chose what send_email did");
    // The namespace and the scheme are noise; the name and the host are not.
    expect(copy.detail).toContain("acme.billing_address");
    expect(copy.detail).toContain("vendor.example/notice");
    expect(copy.detail).not.toContain("https://");
    expect(copy.detail).toContain('the value for "to"');
  });

  it("does not claim a value was chosen when only the context was untrusted", () => {
    const copy = describeAlert({ ...base, severity: "high" });
    expect(copy.headline).toContain("while text from outside was still in view");
  });

  it("has a short form for a narrow list", () => {
    expect(describeAlert(base).short).toBe("send_email used text from outside");
  });

  it("falls back to the recorded title for a kind it does not know", () => {
    const copy = describeAlert({ ...base, kind: "something.new" });
    expect(copy.headline).toBe(base.title);
  });
});

describe("plainPlaceholders", () => {
  it("writes a redaction placeholder out as words", () => {
    expect(plainPlaceholders("mail [EMAIL:3f9a1c] now")).toBe("mail an email address now");
  });

  it("leaves text without placeholders alone", () => {
    expect(plainPlaceholders("nothing hidden here")).toBe("nothing hidden here");
  });
});

describe("shortKey", () => {
  it("keeps the part that tells one memory from another", () => {
    expect(shortKey("crm/memories/acme.billing_address")).toBe("acme.billing_address");
    expect(shortKey("standalone")).toBe("standalone");
  });
});
