import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "@/lib/api";
import { MemoryPage } from "@/routes/MemoryPage";
import { memoryPage, MEMORY_KEY } from "@/test/fixtures";
import { renderAt } from "@/test/render";

describe("the memory page", () => {
  afterEach(() => vi.restoreAllMocks());

  it("lists the versions with their trust and provenance", async () => {
    vi.spyOn(api, "memory").mockResolvedValue(memoryPage);
    renderAt(<MemoryPage />, "/memories/$memoryId");
    // The short name is the heading; the full key is still on the page.
    await waitFor(() => expect(screen.getByText("acme.billing_address")).toBeInTheDocument());
    expect(screen.getByText(new RegExp(MEMORY_KEY))).toBeInTheDocument();

    expect(screen.getByText("v1")).toBeInTheDocument();
    expect(screen.getByText("current")).toBeInTheDocument();
    expect(screen.getByText("From outside")).toBeInTheDocument();
    // "lineage precision step" means nothing to a reader, so it moved to the
    // tooltip on the line that carries it.
    expect(screen.getByText(/^written /).title).toContain("step precision");
    expect(
      screen.getByText(/remembering a contact detail you did not give it/),
    ).toBeInTheDocument();
  });

  it("marks a deleted memory as deleted", async () => {
    const version = memoryPage.versions[0]!;
    vi.spyOn(api, "memory").mockResolvedValue({
      ...memoryPage,
      memory: { ...memoryPage.memory, status: "deleted", current_version: 2 },
      versions: [version, { ...version, id: "v2", version: 2, deleted: true, content: "" }],
    });
    renderAt(<MemoryPage />, "/memories/$memoryId");
    await waitFor(() => expect(screen.getByText("deleted")).toBeInTheDocument());
    expect(screen.getByText("v2")).toBeInTheDocument();
  });
});
