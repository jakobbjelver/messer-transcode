import type { ScheduleWindow, WindowState } from "./types";

export const DAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"] as const;
export const DAY_LABEL: Record<string, string> = {
  mon: "Mon",
  tue: "Tue",
  wed: "Wed",
  thu: "Thu",
  fri: "Fri",
  sat: "Sat",
  sun: "Sun",
};

/** 48 half-hour cells per day: 00:00–00:30 … 23:30–24:00. */
export const CELLS = 48;

/** A painted grid: grid[day][cell] is true when work is allowed. */
export type Grid = boolean[][];

export function emptyGrid(): Grid {
  return DAYS.map(() => Array.from({ length: CELLS }, () => false));
}

function cellOf(minutes: number): number {
  return Math.floor(minutes / 30);
}

function minutesOf(hhmm: string): number {
  const [h, m] = hhmm.split(":").map(Number);
  return (h || 0) * 60 + (m || 0);
}

/** "24:00" for a block that runs to midnight, which is how a day ends. */
function clock(minutes: number): string {
  if (minutes >= 24 * 60) return "00:00";
  return `${String(Math.floor(minutes / 60)).padStart(2, "0")}:${String(minutes % 60).padStart(2, "0")}`;
}

/**
 * Expand windows into the grid the editor paints.
 *
 * A window that crosses midnight lights the cells after its start on its own
 * day and the cells before its end on every day — which is what "Friday 22:00
 * to 06:00" means, and why the grid is the honest editor for it: the block is
 * visible as two pieces of two rows.
 */
export function gridFromWindows(windows: ScheduleWindow[] | undefined): Grid {
  const g = emptyGrid();
  for (const w of windows ?? []) {
    const start = minutesOf(w.start);
    let end = minutesOf(w.end);
    if (end === 0 && start > 0) end = 24 * 60;
    const days = w.days.map((d) => DAYS.indexOf(d as (typeof DAYS)[number])).filter((i) => i >= 0);
    for (const d of days) {
      if (end > start) {
        for (let t = cellOf(start); t < cellOf(end); t++) g[d][t] = true;
      } else {
        // Wraps: the tail of this day, the head of the next.
        for (let t = cellOf(start); t < CELLS; t++) g[d][t] = true;
        const next = (d + 1) % 7;
        for (let t = 0; t < cellOf(end); t++) g[next][t] = true;
      }
    }
  }
  return g;
}

/**
 * Compress the grid back into windows.
 *
 * `previous` is the window list the grid was painted from. Keeping every
 * previous window that still fits — every cell it lit is still lit — is what
 * makes the editor non-destructive: a schedule written as two overlapping
 * blocks stays two blocks when a third is added, instead of being rewritten
 * into the three rectangles that cover the same shape. Only the cells that are
 * not covered by a kept window are newly covered, and those are grouped by the
 * set of days that are on in each half hour.
 *
 * A block that ends at midnight is written as "00:00" — the parser reads that
 * as the end of the day, not the start of the next one.
 */
export function windowsFromGrid(grid: Grid, previous?: ScheduleWindow[]): ScheduleWindow[] {
  const remaining = grid.map((row) => [...row]);
  const out: ScheduleWindow[] = [];

  for (const w of previous ?? []) {
    const cells = cellsOf(w);
    if (cells.some(([d, c]) => !grid[d][c])) continue; // no longer fits
    out.push(w);
    for (const [d, c] of cells) remaining[d][c] = false;
  }

  let runStart = -1;
  let runDays: number[] = [];
  const flush = (endCell: number) => {
    if (runStart < 0 || runDays.length === 0) return;
    out.push({
      days: runDays.map((d) => DAYS[d]),
      start: clock(runStart * 30),
      end: endCell * 30 >= 24 * 60 ? "00:00" : clock(endCell * 30),
    });
    runStart = -1;
    runDays = [];
  };

  for (let cell = 0; cell <= CELLS; cell++) {
    const days = cell === CELLS ? [] : DAYS.map((_, d) => d).filter((d) => remaining[d][cell]);
    const same = runStart >= 0 && days.length === runDays.length && days.every((d, i) => d === runDays[i]);
    if (same) continue;
    flush(cell);
    if (days.length > 0) {
      runStart = cell;
      runDays = days;
    }
  }

  // Two blocks that touch and cover the same days are one block: painting a
  // second half hour next to a first is a continuation, not a new window.
  const merged: ScheduleWindow[] = [];
  const sorted = out.sort(
    (a, b) => minutesOf(a.start) - minutesOf(b.start) || DAYS.indexOf(a.days[0] as never) - DAYS.indexOf(b.days[0] as never),
  );
  for (const w of sorted) {
    const prev = merged[merged.length - 1];
    if (prev && sameDays(prev.days, w.days) && minutesOf(prev.end) === minutesOf(w.start) && minutesOf(prev.end) !== 0) {
      merged[merged.length - 1] = { ...prev, end: w.end };
      continue;
    }
    merged.push(w);
  }
  return merged;
}

function sameDays(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  const set = new Set(a);
  return b.every((d) => set.has(d));
}

/** Every (day, cell) a window lights. */
function cellsOf(w: ScheduleWindow): [number, number][] {
  const out: [number, number][] = [];
  const start = minutesOf(w.start);
  let end = minutesOf(w.end);
  if (end === 0 && start > 0) end = 24 * 60;
  const days = w.days.map((d) => DAYS.indexOf(d as (typeof DAYS)[number])).filter((i) => i >= 0);
  for (const d of days) {
    if (end > start) {
      for (let t = cellOf(start); t < cellOf(end); t++) out.push([d, t]);
    } else {
      for (let t = cellOf(start); t < CELLS; t++) out.push([d, t]);
      const next = (d + 1) % 7;
      for (let t = 0; t < cellOf(end); t++) out.push([next, t]);
    }
  }
  return out;
}

/** "Mon–Fri, 00:30–05:30" — the line under the grid, same voice as the server. */
export function describeWindow(w: ScheduleWindow): string {
  return `${describeDays(w.days)}, ${w.start}–${w.end}`;
}

export function describeDays(days: string[]): string {
  const set = new Set(days);
  if (set.size === 7) return "every day";
  if (set.size === 5 && ["mon", "tue", "wed", "thu", "fri"].every((d) => set.has(d))) return "weekdays";
  if (set.size === 2 && set.has("sat") && set.has("sun")) return "weekends";
  if (set.size === 1) return `on ${DAY_LABEL[days[0]] ?? days[0]}`;
  // A run of two or more days reads as a list, which is what the server says.
  return days.map((d) => DAY_LABEL[d] ?? d).join(", ");
}

/** How long each window is, in hours as a person reads it. */
export function windowHours(w: ScheduleWindow): number {
  const start = minutesOf(w.start);
  let end = minutesOf(w.end);
  if (end === 0 && start > 0) end = 24 * 60;
  return (end >= start ? end - start : 24 * 60 - start + end) / 60;
}

/** The schedule in one line, for a header or a badge. */
export function scheduleLabel(s: { windows?: ScheduleWindow[]; timezone?: string; drain_before_minutes?: number } | null | undefined): string {
  if (!s) return "";
  const tz = s.timezone || "UTC";
  const drain = (s.drain_before_minutes ?? 0) > 0 ? ` · drain ${s.drain_before_minutes}m` : "";
  const windows = s.windows ?? [];
  if (windows.length === 0) return `${tz}${drain} · no window`;
  return `${windows.map(describeWindow).join(" · ")} · ${tz}${drain}`;
}

/** What the window is doing, in one line. */
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
