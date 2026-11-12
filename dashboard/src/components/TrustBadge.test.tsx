import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TrustBadge } from "@/components/TrustBadge";
import { TIER_WORDS } from "@/lib/trust";

describe("TrustBadge", () => {
  it("says where something came from in words, not in tier numbers", () => {
    for (const [value, words] of Object.entries(TIER_WORDS)) {
      const { unmount } = render(<TrustBadge trust={Number(value)} />);
      expect(screen.getByText(words)).toBeInTheDocument();
      unmount();
    }
  });

  it("treats a missing tier as unknown rather than trusted", () => {
    render(<TrustBadge trust={undefined} />);
    expect(screen.getByText("Unknown origin")).toBeInTheDocument();
  });

  it("keeps the exact tier name in the tooltip, with the reason", () => {
    render(<TrustBadge trust={1} reason="document fetched from https://vendor.example" />);
    const badge = screen.getByText("From outside");
    expect(badge.title).toContain("T1 External");
    expect(badge.title).toContain("a page the assistant read");
    expect(badge.title).toContain("vendor.example");
  });
});
