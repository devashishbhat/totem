import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "@/lib/api";
import { Overview } from "@/routes/Overview";
import { overview } from "@/test/fixtures";
import { renderAt } from "@/test/render";

describe("the overview", () => {
  afterEach(() => vi.restoreAllMocks());

  it("puts the incident first, in words a newcomer can read", async () => {
    vi.spyOn(api, "overview").mockResolvedValue(overview);
    renderAt(<Overview />);
    await waitFor(() =>
      expect(screen.getByRole("link", { name: /See how this happened/ })).toBeInTheDocument(),
    );
    // Not "Untrusted memory chose `to` of send_email", which is exact and
    // means nothing to someone reading their first alert. It appears once:
    // the banner is the alert, so the list below it holds the others.
    expect(
      screen.getAllByText("Something the assistant read chose what send_email did"),
    ).toHaveLength(1);
    expect(screen.getByText(/You did not supply the value for "to"/)).toBeInTheDocument();
    expect(screen.getByText("Also worth knowing")).toBeInTheDocument();
  });

  it("explains what the application is for", async () => {
    vi.spyOn(api, "overview").mockResolvedValue(overview);
    renderAt(<Overview />);
    await waitFor(() =>
      expect(screen.getByText(/Totem records where every action came from/)).toBeInTheDocument(),
    );
  });

  it("teaches the colours it uses", async () => {
    vi.spyOn(api, "overview").mockResolvedValue(overview);
    renderAt(<Overview />);
    await waitFor(() => expect(screen.getByText("From you")).toBeInTheDocument());
    expect(screen.getByText("From outside")).toBeInTheDocument();
  });

  it("does not call somebody else's alert a poisoning", async () => {
    vi.spyOn(api, "overview").mockResolvedValue({ ...overview, demo_mode: false });
    renderAt(<Overview />);
    await waitFor(() =>
      expect(screen.getByRole("link", { name: /See what caused this/ })).toBeInTheDocument(),
    );
  });

  it("counts what was recorded, in nouns a newcomer knows", async () => {
    vi.spyOn(api, "overview").mockResolvedValue(overview);
    renderAt(<Overview />);
    await waitFor(() => expect(screen.getByText("Conversations")).toBeInTheDocument());
    expect(screen.getByText("Things it did")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument();
  });

  it("tells a fresh clone how to start", async () => {
    vi.spyOn(api, "overview").mockResolvedValue({
      ...overview,
      alerts: [],
      featured: null,
      runs: 0,
      decisions: 0,
      memories: 0,
    });
    renderAt(<Overview />);
    await waitFor(() =>
      expect(screen.getByText(/Nothing has been recorded yet/)).toBeInTheDocument(),
    );
    expect(screen.getByText("make start")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /See what caused/ })).toBeNull();
  });

  it("says so when an agent has run but nothing is wrong", async () => {
    vi.spyOn(api, "overview").mockResolvedValue({ ...overview, alerts: [], featured: null });
    renderAt(<Overview />);
    await waitFor(() =>
      expect(screen.getByText(/has not acted on anything untrusted/)).toBeInTheDocument(),
    );
    expect(screen.queryByRole("link", { name: /See what caused/ })).toBeNull();
  });

  it("says when the API is unreachable", async () => {
    vi.spyOn(api, "overview").mockRejectedValue(new Error("Failed to fetch"));
    renderAt(<Overview />);
    await waitFor(() =>
      expect(screen.getByText("The API could not be reached.")).toBeInTheDocument(),
    );
  });
});
