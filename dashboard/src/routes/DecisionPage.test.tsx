import { screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/lib/api";
import { DecisionPage } from "@/routes/DecisionPage";
import { explain, VENDOR_URL } from "@/test/fixtures";
import { renderAt } from "@/test/render";

describe("the decision page", () => {
  beforeEach(() => {
    vi.spyOn(api, "explain").mockResolvedValue(explain);
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  async function show() {
    renderAt(<DecisionPage />, "/decisions/$decisionId");
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "send_email()" })).toBeInTheDocument(),
    );
  }

  it("leads with the alert and what the action was", async () => {
    await show();
    expect(
      screen.getByText("Something the assistant read chose what send_email did"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("critical").length).toBeGreaterThan(0);
    expect(screen.getByText(/cannot be taken back/)).toBeInTheDocument();
  });

  it("explains the hidden values rather than leaving them as codes", async () => {
    await show();
    expect(screen.getByText(/The same value always gets the same code/)).toBeInTheDocument();
  });

  it("shows each argument, with the redacted address as a chip", async () => {
    await show();
    expect(screen.getByText("to")).toBeInTheDocument();
    expect(screen.getByText("October invoice.")).toBeInTheDocument();
    // The same code appears wherever that address went: the argument, the
    // memory, the path and the replay samples. That is the point of it.
    expect(screen.getAllByText("837497").length).toBeGreaterThan(1);
  });

  it("states each level of evidence in the agreed words", async () => {
    await show();
    // These four strings are the product's claims, and their wording is fixed:
    // they are what Totem asserts, so they do not get reworded casually.
    expect(screen.getByText("Retrieved")).toBeInTheDocument();
    // The user's message was in the prompt too, so this label appears twice.
    expect(screen.getAllByText("Was in the prompt").length).toBe(2);
    expect(screen.getByText("Supplied a value to `to`")).toBeInTheDocument();
    expect(
      screen.getByText("Removing it changed the decision in 8 of 8 samples"),
    ).toBeInTheDocument();
  });

  it("draws the path from the decision back to the vendor page", async () => {
    await show();
    const path = screen.getByLabelText("Where this value came from");
    expect(path).toBeInTheDocument();
    // The namespace is the same for every memory, so the card shows the part
    // that tells one memory from another.
    expect(path.textContent).toContain("acme.billing_address");
    expect(path.textContent).toContain(VENDOR_URL);
    // Both hops came from outside: that is what makes this an incident.
    expect(path.textContent).toContain("From outside");
  });

  it("shows the replay with both arms and the verdict", async () => {
    await show();
    const card = screen.getByTestId("replay-card");
    expect(card.textContent).toContain("With the memory");
    expect(card.textContent).toContain("Without the memory");
    expect(screen.getByTestId("replay-sentence").textContent).toContain(
      "changed the decision in 8 of 8 samples",
    );
    expect(screen.getByText("strong")).toBeInTheDocument();
    expect(screen.getByText("8 of 8")).toBeInTheDocument();
  });

  it("offers no replay button on the read-only demo", async () => {
    await show();
    expect(screen.queryByRole("button", { name: /Replay without this memory/ })).toBeNull();
  });

  it("says what went wrong when the decision cannot be loaded", async () => {
    vi.spyOn(api, "explain").mockRejectedValue(new Error("404 not found"));
    renderAt(<DecisionPage />, "/decisions/$decisionId");
    await waitFor(() =>
      expect(screen.getByText("This decision could not be loaded.")).toBeInTheDocument(),
    );
  });
});
