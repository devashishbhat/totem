import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ObservedText } from "@/components/ObservedText";

describe("ObservedText", () => {
  it("renders markup in observed text as text, never as HTML", () => {
    // Memory and tool output are untrusted: a page that stores a script tag
    // must be shown, not run.
    const { container } = render(
      <ObservedText text={'<img src=x onerror="alert(1)">  <b>bold</b>'} />,
    );
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("b")).toBeNull();
    expect(container.textContent).toContain('<img src=x onerror="alert(1)">');
    expect(container.textContent).toContain("<b>bold</b>");
  });

  it("does not interpret markdown", () => {
    const { container } = render(<ObservedText text={"# heading\n- [link](https://x.example)"} />);
    expect(container.querySelector("h1")).toBeNull();
    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).toContain("# heading");
  });

  it("shows a redaction placeholder as a chip while keeping the rest as text", () => {
    render(<ObservedText text="Invoices for Acme go to [EMAIL:837497] now" />);
    expect(screen.getByText("837497")).toBeInTheDocument();
    expect(screen.getByText("EMAIL")).toBeInTheDocument();
    expect(screen.getByText(/Invoices for Acme go to/)).toBeInTheDocument();
    expect(screen.getByText(/now/)).toBeInTheDocument();
  });

  it("shows every placeholder in a line", () => {
    render(<ObservedText text="from [EMAIL:aaaaaa] to [EMAIL:bbbbbb] via [PHONE:cccccc]" />);
    expect(screen.getByText("aaaaaa")).toBeInTheDocument();
    expect(screen.getByText("bbbbbb")).toBeInTheDocument();
    expect(screen.getByText("cccccc")).toBeInTheDocument();
  });

  it("leaves text that only looks like a placeholder alone", () => {
    const { container } = render(<ObservedText text="[EMAIL:NOTHEX] and [OTHER:abcdef]" />);
    expect(container.textContent).toBe("[EMAIL:NOTHEX] and [OTHER:abcdef]");
  });
});
