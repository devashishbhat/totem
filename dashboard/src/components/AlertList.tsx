import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { ObservedText } from "@/components/ObservedText";
import { Dot, Empty } from "@/components/ui";
import type { Alert } from "@/lib/api";
import { cn } from "@/lib/cn";
import { describeAlert } from "@/lib/describe";
import { formatRelative } from "@/lib/format";
import { SEVERITY_TEXT, SEVERITY_TONE } from "@/lib/trust";

/** What each severity is asking of the reader. */
const SEVERITY_MEANS: Record<string, string> = {
  critical: "Something outside the assistant decided what it did. Worth looking at now.",
  high: "Text from outside was in play when the assistant acted. Worth a look.",
  medium: "Worth knowing about.",
  low: "For the record.",
};

function targetLink(alert: Alert) {
  switch (alert.target.kind) {
    case "decision":
      return { to: "/decisions/$decisionId", params: { decisionId: alert.target.id } } as const;
    case "memory":
      return { to: "/memories/$memoryId", params: { memoryId: alert.target.id } } as const;
    default:
      return { to: "/sources/$sourceId", params: { sourceId: alert.target.id } } as const;
  }
}

const ROW = "flex items-start gap-3 px-4 py-3.5";

/**
 * The alerts for one subject.
 *
 * `hereId` is the thing the surrounding page already shows. An alert about it
 * still belongs on the page, but it must not offer to take you where you
 * already are: a row that navigates to the current URL is a button that does
 * nothing, which is exactly how it reads.
 */
export function AlertList({ alerts, hereId }: { alerts: Alert[]; hereId?: string }) {
  if (alerts.length === 0) {
    return <Empty>Nothing to report. The assistant has not acted on anything untrusted.</Empty>;
  }
  return (
    <ul className="divide-y divide-line-soft">
      {alerts.map((alert) => {
        const here = alert.target.id === hereId;
        const body = <AlertBody alert={alert} here={here} />;
        return (
          <li key={alert.id}>
            {here ? (
              <div className={ROW}>{body}</div>
            ) : (
              <Link {...targetLink(alert)} className={cn(ROW, "transition-colors hover:bg-card/50")}>
                {body}
              </Link>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function AlertBody({ alert, here }: { alert: Alert; here: boolean }): ReactNode {
  const copy = describeAlert(alert);
  return (
    <>
      <Dot
        tone={SEVERITY_TONE[alert.severity] ?? "muted"}
        className="mt-[0.4rem]"
        title={SEVERITY_MEANS[alert.severity]}
      />
      <div className="min-w-0 flex-1 space-y-1">
        <ObservedText text={copy.headline} className="text-sm text-ink" as="p" />
        {copy.detail ? (
          <ObservedText text={copy.detail} className="text-xs text-ink-muted" as="p" />
        ) : null}
        <p className="flex flex-wrap items-center gap-x-2 text-xs text-ink-faint">
          <span
            className={cn("font-medium", SEVERITY_TEXT[alert.severity])}
            title={SEVERITY_MEANS[alert.severity]}
          >
            {alert.severity}
          </span>
          <span aria-hidden="true">·</span>
          <time dateTime={alert.created_at}>{formatRelative(alert.created_at)}</time>
        </p>
      </div>
      <span className={cn("shrink-0 self-center text-xs", here ? "text-ink-faint" : "text-accent")}>
        {here ? "you are here" : "Look"}
      </span>
    </>
  );
}
