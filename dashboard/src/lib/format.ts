/** Formatting helpers shared by the pages. */

export function formatTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

/**
 * How far into the run something happened. An agent run takes well under a
 * second, so wall-clock times all read the same and the order is what matters.
 */
export function formatOffset(iso: string, startIso: string): string {
  const at = new Date(iso).getTime();
  const start = new Date(startIso).getTime();
  if (Number.isNaN(at) || Number.isNaN(start)) return "";
  const seconds = Math.max(0, (at - start) / 1000);
  return `+${seconds.toFixed(3)}s`;
}

export function formatRelative(iso: string, now: Date = new Date()): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const seconds = Math.round((now.getTime() - date.getTime()) / 1000);
  const units: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, "second"],
    [3600, "minute"],
    [86400, "hour"],
    [2592000, "day"],
  ];
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  if (seconds < 60) return formatter.format(-seconds, "second");
  for (let i = 1; i < units.length; i += 1) {
    const [limit, unit] = units[i]!;
    const [previous] = units[i - 1]!;
    if (seconds < limit) return formatter.format(-Math.round(seconds / previous), unit);
  }
  return formatter.format(-Math.round(seconds / 86400), "day");
}

/** A signed percentage, for the replay effect. */
export function formatEffect(value: number | null): string {
  if (value === null) return "not measured";
  return `${value >= 0 ? "+" : ""}${Math.round(value * 100)}%`;
}

export function shortId(id: string): string {
  return id.slice(0, 8);
}

/** A relative time short enough for a sidebar: 3m, 2h, 4d. */
export function formatRelativeShort(iso: string, now: Date = new Date()): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const seconds = Math.max(0, Math.round((now.getTime() - date.getTime()) / 1000));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

/** "1 memory", "2 memories". */
export function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
}
