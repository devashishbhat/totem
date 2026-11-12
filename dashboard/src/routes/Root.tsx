import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useRouterState } from "@tanstack/react-router";
import { LayoutGrid, Lock, Play } from "lucide-react";

import { Dot } from "@/components/ui";
import { api } from "@/lib/api";
import { cn } from "@/lib/cn";
import { describeAlert, plainPlaceholders } from "@/lib/describe";
import { formatRelativeShort, plural } from "@/lib/format";
import { SEVERITY_TONE, tierTone } from "@/lib/trust";

/**
 * The shell: a sidebar that always says what there is, and a single content
 * pane. The sidebar is the thing you keep open, so it carries the alert count
 * and the recent runs rather than making you navigate to find them.
 */
export function Root() {
  const overview = useQuery({ queryKey: ["overview"], queryFn: api.overview });
  return (
    <div className="flex min-h-screen bg-bg">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <MobileBar />
        {overview.data?.demo_mode ? <DemoBanner /> : null}
        <main className="min-w-0 flex-1 px-4 py-5 sm:px-6 sm:py-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

/**
 * On the public demo, the first thing a visitor needs is to know what they are
 * looking at. One quiet line, not a poster.
 */
function DemoBanner() {
  return (
    <div className="flex items-start gap-2.5 border-b border-line-soft bg-panel px-4 py-2.5 sm:px-6">
      <Dot tone="accent" className="mt-[0.4rem]" />
      <p className="text-xs text-ink-muted">
        <span className="text-ink">This is a recorded run.</span> A CRM assistant read a vendor page
        that asked it to remember a billing address, then emailed that address an invoice.
        Everything here is read-only.
      </p>
    </div>
  );
}

function Sidebar() {
  const overview = useQuery({ queryKey: ["overview"], queryFn: api.overview });
  const runs = useQuery({ queryKey: ["runs"], queryFn: api.runs });
  const alerts = overview.data?.alerts ?? [];

  return (
    <aside className="sticky top-0 hidden h-screen w-72 shrink-0 flex-col border-r border-line-soft bg-sidebar md:flex">
      <Link to="/" className="flex items-center gap-2 px-4 py-4">
        <Totem />
        <span className="text-sm font-semibold tracking-tight text-ink">Totem</span>
      </Link>

      <nav className="px-2">
        <NavItem to="/" icon={<LayoutGrid className="size-3.5" />} label="Overview" />
        <NavItem
          to="/runs"
          icon={<Play className="size-3.5" />}
          label="Conversations"
          count={runs.data?.runs.length}
        />
      </nav>

      {alerts.length > 0 ? (
        <Group
          // The count labels the list below it, so it counts the whole list;
          // severity is what each row's dot carries.
          label="Alerts"
          count={alerts.length}
        >
          {alerts.slice(0, 6).map((alert) => (
            <SidebarRow
              key={alert.id}
              to={
                alert.target.kind === "decision"
                  ? { to: "/decisions/$decisionId", params: { decisionId: alert.target.id } }
                  : { to: "/memories/$memoryId", params: { memoryId: alert.target.id } }
              }
              tone={SEVERITY_TONE[alert.severity] ?? "muted"}
              title={describeAlert(alert).short}
              meta={formatRelativeShort(alert.created_at)}
            />
          ))}
        </Group>
      ) : null}

      {runs.data && runs.data.runs.length > 0 ? (
        <Group label="Recent conversations">
          {runs.data.runs.slice(0, 8).map((run) => (
            <SidebarRow
              key={run.id}
              to={{ to: "/runs/$runId", params: { runId: run.id } }}
              tone={tierTone(run.trust_min)}
              // What was said, not the id. An id tells you nothing about which
              // conversation this was, and a list of them is unreadable.
              title={
                run.opening
                  ? plainPlaceholders(run.opening)
                  : `Run ${run.id.replace(/^run-/, "").slice(0, 8)}`
              }
              mono={!run.opening}
              meta={formatRelativeShort(run.started_at)}
            />
          ))}
        </Group>
      ) : null}

      <div className="mt-auto border-t border-line-soft px-4 py-3">
        {overview.data?.demo_mode ? (
          <p className="flex items-center gap-2 text-xs text-ink-faint">
            <Lock className="size-3" aria-hidden="true" />
            Read-only demo
          </p>
        ) : (
          <p className="text-xs text-ink-faint">
            {overview.data
              ? `${plural(overview.data.decisions, "action", "actions")} · ${plural(
                  overview.data.memories,
                  "memory",
                  "memories",
                )}`
              : " "}
          </p>
        )}
      </div>
    </aside>
  );
}

/** The mark: a stack that narrows, like a decision traced back to one source. */
function Totem() {
  return (
    <svg viewBox="0 0 16 16" className="size-4" aria-hidden="true">
      <rect x="1" y="2" width="14" height="3" rx="1.5" className="fill-trust-1" />
      <rect x="3" y="6.5" width="10" height="3" rx="1.5" className="fill-trust-2/80" />
      <rect x="5.5" y="11" width="5" height="3" rx="1.5" className="fill-accent" />
    </svg>
  );
}

function MobileBar() {
  const overview = useQuery({ queryKey: ["overview"], queryFn: api.overview });
  return (
    <header className="flex items-center gap-4 border-b border-line-soft bg-sidebar px-4 py-3 md:hidden">
      <Link to="/" className="flex items-center gap-2">
        <Totem />
        <span className="text-sm font-semibold text-ink">Totem</span>
      </Link>
      <nav className="flex items-center gap-1 text-sm">
        <NavItem to="/" label="Overview" compact />
        <NavItem to="/runs" label="Runs" compact />
      </nav>
      {overview.data?.demo_mode ? (
        <span className="ml-auto flex items-center gap-1.5 text-xs text-ink-faint">
          <Lock className="size-3" aria-hidden="true" />
          Demo
        </span>
      ) : null}
    </header>
  );
}

function NavItem({
  to,
  label,
  icon,
  count,
  compact,
}: {
  to: "/" | "/runs";
  label: string;
  icon?: React.ReactNode;
  count?: number;
  compact?: boolean;
}) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const active = to === "/" ? pathname === "/" : pathname.startsWith(to);
  return (
    <Link
      to={to}
      className={cn(
        "flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm transition-colors",
        compact ? "" : "w-full",
        active ? "bg-card text-ink" : "text-ink-muted hover:bg-card/60 hover:text-ink",
      )}
    >
      {icon}
      {label}
      {count !== undefined ? <span className="ml-auto text-xs text-ink-faint">{count}</span> : null}
    </Link>
  );
}

function Group({
  label,
  count,
  children,
}: {
  label: string;
  count?: number;
  children: React.ReactNode;
}) {
  return (
    <div className="mt-5 min-h-0">
      <div className="flex items-baseline justify-between px-4 pb-1.5">
        <span className="text-xs text-ink-faint">{label}</span>
        {count !== undefined ? <span className="text-xs text-ink-faint">{count}</span> : null}
      </div>
      <div className="px-2">{children}</div>
    </div>
  );
}

type SidebarLink =
  | { to: "/decisions/$decisionId"; params: { decisionId: string } }
  | { to: "/memories/$memoryId"; params: { memoryId: string } }
  | { to: "/runs/$runId"; params: { runId: string } };

function SidebarRow({
  to,
  tone,
  title,
  meta,
  mono,
}: {
  to: SidebarLink;
  tone: "trust0" | "trust1" | "trust2" | "trust3" | "trust4" | "accent" | "muted";
  title: string;
  meta: string;
  mono?: boolean;
}) {
  return (
    <Link
      {...to}
      className="group flex items-start gap-2 rounded-md px-2.5 py-2 transition-colors hover:bg-card/60"
      title={title}
    >
      <Dot tone={tone} className="mt-[0.3rem]" />
      <span
        className={cn(
          "min-w-0 flex-1 text-[0.8125rem] leading-snug text-ink-muted group-hover:text-ink",
          "line-clamp-2",
          mono && "truncate font-mono",
        )}
      >
        {title}
      </span>
      <span className="shrink-0 text-xs text-ink-faint">{meta}</span>
    </Link>
  );
}
