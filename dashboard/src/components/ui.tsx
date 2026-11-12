/**
 * The vocabulary the pages are built from.
 *
 * The rules, so that every screen reads the same way: sections are named by a
 * small quiet label rather than a boxed header; status is a coloured dot and a
 * word, never a heavy badge; identifiers are monospace; and colour is reserved
 * for things that mean something, which here is trust and severity.
 */

import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps, ReactNode } from "react";

import { cn } from "@/lib/cn";

/** A small, quiet heading that names a section without shouting. */
export function SectionLabel({
  children,
  className,
  aside,
}: {
  children: ReactNode;
  className?: string;
  aside?: ReactNode;
}) {
  return (
    <div className={cn("flex items-baseline justify-between gap-3", className)}>
      <h2 className="text-xs font-medium text-ink-faint">{children}</h2>
      {aside}
    </div>
  );
}

/** A panel: the one container. No nested boxes. */
export function Panel({ className, ...props }: ComponentProps<"section">) {
  return (
    <section className={cn("rounded-lg border border-line-soft bg-panel", className)} {...props} />
  );
}

export function PanelHeader({
  title,
  subtitle,
  aside,
  className,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  aside?: ReactNode;
  className?: string;
}) {
  return (
    <header
      className={cn(
        "flex flex-wrap items-start justify-between gap-x-4 gap-y-2 border-b border-line-soft px-4 py-3",
        className,
      )}
    >
      <div className="min-w-0">
        <h2 className="text-sm font-medium text-ink">{title}</h2>
        {subtitle ? <p className="mt-0.5 text-xs text-ink-faint">{subtitle}</p> : null}
      </div>
      {aside}
    </header>
  );
}

export function PanelBody({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("px-4 py-3", className)} {...props} />;
}

const dot = cva("inline-block size-1.5 shrink-0 rounded-full", {
  variants: {
    tone: {
      trust0: "bg-trust-0",
      trust1: "bg-trust-1",
      trust2: "bg-trust-2",
      trust3: "bg-trust-3",
      trust4: "bg-trust-4",
      accent: "bg-accent",
      muted: "bg-ink-faint",
    },
  },
  defaultVariants: { tone: "muted" },
});

/** Status is a dot and a word. */
export function Dot({
  tone,
  className,
  title,
}: VariantProps<typeof dot> & { className?: string; title?: string }) {
  // With a title the dot carries meaning on hover, so it is no longer purely
  // decorative and keeps its place in the accessibility tree.
  return (
    <span
      className={cn(dot({ tone }), className)}
      title={title}
      aria-hidden={title ? undefined : "true"}
    />
  );
}

/** An identifier: a key, a branch, a path, an id. */
export function Mono({ className, ...props }: ComponentProps<"span">) {
  return <span className={cn("font-mono text-xs text-ink-muted", className)} {...props} />;
}

const button = cva(
  "inline-flex items-center justify-center gap-2 rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent",
  {
    variants: {
      variant: {
        primary: "bg-primary text-primary-ink hover:brightness-110",
        secondary: "border border-line bg-card text-ink hover:bg-card-hover",
        ghost: "text-ink-muted hover:bg-card hover:text-ink",
      },
      size: {
        md: "h-8 px-3 text-sm",
        sm: "h-7 px-2.5 text-xs",
      },
    },
    defaultVariants: { variant: "primary", size: "md" },
  },
);

export function Button({
  className,
  variant,
  size,
  ...props
}: ComponentProps<"button"> & VariantProps<typeof button>) {
  return <button className={cn(button({ variant, size }), className)} {...props} />;
}

/** A labelled value in a metadata row. */
export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-ink-faint">{label}</dt>
      <dd className="mt-1 truncate text-sm text-ink">{children}</dd>
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="px-4 py-10 text-center text-sm text-ink-faint">{children}</p>;
}

export function Spinner({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-2.5 px-4 py-10 text-sm text-ink-faint" role="status">
      <span
        className="size-3.5 animate-spin rounded-full border-2 border-line border-t-accent"
        aria-hidden="true"
      />
      {label}
    </div>
  );
}

export function ErrorNote({ title, detail }: { title: string; detail?: string }) {
  return (
    <Panel className="border-trust-0/30">
      <PanelBody className="flex items-start gap-2.5">
        <Dot tone="trust0" className="mt-1.5" />
        <div className="min-w-0">
          <p className="text-sm text-trust-0">{title}</p>
          {detail ? <p className="observed mt-1 text-xs text-ink-muted">{detail}</p> : null}
        </div>
      </PanelBody>
    </Panel>
  );
}
