import type { Schedule, WindowState } from "./types";

/** The one line that says when this thing is allowed to work. */
export function scheduleLabel(s: Schedule | null | undefined): string {
  if (!s) return "";
  const tz = s.timezone || "UTC";
  const drain = s.drain_before_minutes > 0 ? ` · drain ${s.drain_before_minutes}m` : "";
  return `weekdays ${s.weekday} · weekends ${s.weekend} · ${tz}${drain}`;
}

export function windowLabel(w: WindowState | null | undefined): string {
  if (!w) return "";
  return w.open ? `open · ${w.spec}` : `shut · opens ${w.next_at ?? "?"}`;
}

/** What to say when nothing is encoding. */
export function idleNote(w: WindowState | null | undefined): string {
  if (!w) return "No window state yet.";
  if (w.open) return "The window is open — start the node and it will claim work.";
  return `Next window opens at ${w.next_at ?? "?"}. Nothing is queued to a machine that is not running.`;
}
