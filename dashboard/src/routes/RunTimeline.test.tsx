import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "@/lib/api";
import { RunTimeline } from "@/routes/RunTimeline";
import { timeline } from "@/test/fixtures";
import { renderAt } from "@/test/render";

describe("the run timeline", () => {
  afterEach(() => vi.restoreAllMocks());

  it("is titled by what was said, not by an id", async () => {
    vi.spyOn(api, "timeline").mockResolvedValue(timeline);
    renderAt(<RunTimeline />, "/runs/$runId");
    await waitFor(() =>
      expect(screen.getByText("Send the October invoice to Acme.")).toBeInTheDocument(),
    );
  });

  it("shows the steps in order, each saying where it came from", async () => {
    vi.spyOn(api, "timeline").mockResolvedValue(timeline);
    renderAt(<RunTimeline />, "/runs/$runId");
    await waitFor(() => expect(screen.getAllByRole("listitem").length).toBe(3));

    const items = screen.getAllByRole("listitem");
    // The run takes well under a second, so each step is shown as an offset
    // from the run's start rather than a wall-clock time.
    expect(items[0]!.textContent).toContain("+1.000s");
    expect(items[0]!.textContent).toContain("input");
    expect(items[1]!.textContent).toContain("memory");
    expect(items[2]!.textContent).toContain("action");
    for (const item of items) {
      expect(item.textContent).toContain("From outside");
    }
  });

  it("hides the plumbing until it is asked for", async () => {
    vi.spyOn(api, "timeline").mockResolvedValue({
      ...timeline,
      items: [
        ...timeline.items,
        {
          kind: "llm.call",
          id: "call-1",
          at: timeline.items[0]!.at,
          title: "Asked gpt-4o-mini",
        },
      ],
    });
    renderAt(<RunTimeline />, "/runs/$runId");
    // A model call is not what someone means by "what did it do".
    await waitFor(() => expect(screen.getAllByRole("listitem").length).toBe(3));
    const toggle = screen.getByRole("button", { name: /Show every step/ });
    toggle.click();
    await waitFor(() => expect(screen.getAllByRole("listitem").length).toBe(4));
    expect(screen.getByText("Asked gpt-4o-mini")).toBeInTheDocument();
  });

  it("links a decision to where its value came from", async () => {
    vi.spyOn(api, "timeline").mockResolvedValue(timeline);
    renderAt(<RunTimeline />, "/runs/$runId");
    await waitFor(() =>
      expect(screen.getByRole("link", { name: "Where did this come from?" })).toBeInTheDocument(),
    );
    expect(
      screen.getByRole("link", { name: /Where did this memory come from/ }),
    ).toBeInTheDocument();
  });

  it("says so when a conversation recorded nothing", async () => {
    vi.spyOn(api, "timeline").mockResolvedValue({ ...timeline, items: [] });
    renderAt(<RunTimeline />, "/runs/$runId");
    await waitFor(() =>
      expect(screen.getByText("Nothing was recorded for this conversation.")).toBeInTheDocument(),
    );
  });
});
