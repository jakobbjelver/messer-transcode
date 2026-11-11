import { useCallback, useEffect, useRef, useState } from "react";

import { DAYS, DAY_LABEL, CELLS, describeWindow, gridFromWindows, windowsFromGrid } from "@/lib/schedule";
import type { Grid } from "@/lib/schedule";
import type { ScheduleWindow } from "@/lib/types";

/**
 * The schedule editor: seven rows of half hours, painted.
 *
 * Two things make this a better editor than a pair of text fields. It shows the
 * whole week at once, so "Friday night only" is a shape rather than a sentence
 * to parse; and it cannot express a schedule the model cannot store, because
 * the grid is compressed back into windows on every change.
 *
 * Drag paints (pointerdown, pointerenter), and a drag that starts on a lit cell
 * erases — the behaviour of every calendar a person has used.
 */
export function ScheduleGrid({
  windows,
  onChange,
}: {
  windows: ScheduleWindow[] | undefined;
  onChange: (next: ScheduleWindow[]) => void;
}) {
  const [grid, setGrid] = useState<Grid>(() => gridFromWindows(windows));
  const painting = useRef<{ value: boolean } | null>(null);
  const lastEmitted = useRef<string>(JSON.stringify(windows ?? []));

  // The draft is the source while the user is not painting: a document edit or
  // a discard must be able to move the grid.
  useEffect(() => {
    const key = JSON.stringify(windows ?? []);
    if (key === lastEmitted.current) return;
    lastEmitted.current = key;
    setGrid(gridFromWindows(windows));
  }, [windows]);

  const commit = useCallback(
    (next: Grid) => {
      const out = windowsFromGrid(next, windows ?? []);
      lastEmitted.current = JSON.stringify(out);
      onChange(out);
    },
    [onChange, windows],
  );

  const paint = (day: number, cell: number, value: boolean) => {
    setGrid((prev) => {
      if (prev[day][cell] === value) return prev;
      const next = prev.map((row) => [...row]);
      next[day][cell] = value;
      commit(next);
      return next;
    });
  };

  const start = (day: number, cell: number) => {
    painting.current = { value: !grid[day][cell] };
    paint(day, cell, painting.current.value);
  };

  const enter = (day: number, cell: number) => {
    if (!painting.current) return;
    paint(day, cell, painting.current.value);
  };

  useEffect(() => {
    const stop = () => {
      painting.current = null;
    };
    window.addEventListener("pointerup", stop);
    window.addEventListener("pointercancel", stop);
    return () => {
      window.removeEventListener("pointerup", stop);
      window.removeEventListener("pointercancel", stop);
    };
  }, []);

  const blocks = windowsFromGrid(grid);
  const total = blocks.reduce((n, w) => n + w.days.length * (windowLen(w) / 3600), 0);

  return (
    <div className="flex flex-col gap-3">
      <div className="overflow-x-auto">
        <div className="inline-flex min-w-full flex-col gap-0.5 select-none">
          {/* Six labels, eight cells apart: 00, 04, 08, 12, 16, 20. Every
              half hour labelled would be unreadable at this density, and the
              hour lines are what the eye uses to find a cell anyway. */}
          <div className="grid grid-cols-[2.5rem_repeat(48,minmax(0,1fr))] gap-0.5">
            <span />
            {Array.from({ length: 6 }, (_, i) => (
              <span
                key={i}
                className="m-data col-span-8 border-l border-[var(--border)] pl-1 text-[10px] text-[var(--text-3)]"
              >
                {String(i * 4).padStart(2, "0")}
              </span>
            ))}
          </div>
          {DAYS.map((day, d) => (
            <div key={day} className="grid grid-cols-[2.5rem_repeat(48,minmax(0,1fr))] gap-0.5">
              <span className="m-label flex items-center text-[var(--text-2)]">{DAY_LABEL[day]}</span>
              {Array.from({ length: CELLS }, (_, c) => {
                const on = grid[d][c];
                const quarter = c % 8 === 0;
                return (
                  <button
                    key={c}
                    type="button"
                    tabIndex={-1}
                    aria-label={`${DAY_LABEL[day]} ${stamp(c)} ${on ? "on" : "off"}`}
                    aria-pressed={on}
                    onPointerDown={(e) => {
                      e.preventDefault();
                      start(d, c);
                    }}
                    onPointerEnter={() => enter(d, c)}
                    className={[
                      "h-5 w-full cursor-crosshair transition-colors duration-75",
                      on ? "bg-[var(--foreground)]/80 hover:bg-[var(--foreground)]" : "bg-[var(--surface-2)] hover:bg-[var(--surface-2)]/70",
                      quarter ? "border-l border-[var(--border)]" : "border-0",
                    ]
                      .join(" ")
                      .trim()}
                  />
                );
              })}
            </div>
          ))}
        </div>
      </div>

      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="m-meta text-[var(--text-3)]">
          {blocks.length === 0
            ? "nothing is painted — the node will never claim work"
            : `${blocks.length} block${blocks.length === 1 ? "" : "s"} · ${Math.round(total)} h of window per week`}
        </span>
        <span className="m-meta text-[var(--text-3)]">drag to paint · drag over a painted block to erase</span>
      </div>

      <ul className="flex flex-col divide-y rounded-lg border text-sm">
        {blocks.length === 0 ? (
          <li className="px-3 py-2 text-[var(--text-3)]">No window: the queue will hold every job.</li>
        ) : (
          blocks.map((w, i) => (
            <li key={i} className="flex items-baseline justify-between gap-3 px-3 py-2">
              <span>{describeWindow(w)}</span>
              <span className="m-data text-[var(--text-3)]">{windowLen(w) / 3600} h</span>
            </li>
          ))
        )}
      </ul>
    </div>
  );
}

/** "00:30" for a half-hour cell index. */
function stamp(cell: number): string {
  const m = cell * 30;
  return `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
}

function windowLen(w: ScheduleWindow): number {
  const mins = (s: string) => {
    const [h, m] = s.split(":").map(Number);
    return h * 60 + m;
  };
  const a = mins(w.start);
  let b = mins(w.end);
  if (b === 0 && a > 0) b = 24 * 60;
  return (b >= a ? b - a : 24 * 60 - a + b) * 60;
}
