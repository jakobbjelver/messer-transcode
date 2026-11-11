import { describe, expect, it } from "vitest";

import {
  DAYS,
  describeWindow,
  emptyGrid,
  gridFromWindows,
  idleNote,
  scheduleLabel,
  windowHours,
  windowLabel,
  windowsFromGrid,
} from "./schedule";
import type { ScheduleWindow } from "./types";

const schedule = {
  windows: [
    { days: ["mon", "tue", "wed", "thu", "fri"], start: "00:30", end: "05:30" },
    { days: ["sat", "sun"], start: "02:00", end: "07:00" },
  ] as ScheduleWindow[],
  drain_before_minutes: 15,
  timezone: "Europe/Stockholm",
};

describe("scheduleLabel", () => {
  it("states each window, the zone and the drain", () =>
    expect(scheduleLabel(schedule)).toBe(
      "weekdays, 00:30–05:30 · weekends, 02:00–07:00 · Europe/Stockholm · drain 15m",
    ));
  it("drops the drain when there is none", () =>
    expect(scheduleLabel({ ...schedule, drain_before_minutes: 0 })).not.toContain("drain"));
  it("falls back to UTC when the zone is empty", () =>
    expect(scheduleLabel({ ...schedule, timezone: "" })).toContain("UTC"));
  it("renders nothing without a schedule", () => expect(scheduleLabel(null)).toBe(""));
});

describe("grid round trip", () => {
  it("puts the weekday window on the right cells", () => {
    const g = gridFromWindows([{ days: ["mon"], start: "00:30", end: "05:30" }]);
    expect(g[0][0]).toBe(false); // 00:00-00:30
    expect(g[0][1]).toBe(true); // 00:30-01:00
    expect(g[0][10]).toBe(true); // 05:00-05:30
    expect(g[0][11]).toBe(false); // 05:30-06:00
    expect(g[1][1]).toBe(false); // tuesday untouched
  });

  it("keeps the windows it was given, so painting does not rewrite them", () => {
    const g = gridFromWindows(schedule.windows);
    expect(windowsFromGrid(g, schedule.windows)).toEqual(schedule.windows);
  });

  it("covers the same half hours when it has no memory", () => {
    // Without the previous list the same shape is covered by three rectangles:
    // the union of two overlapping blocks is not two rectangles.
    const g = gridFromWindows(schedule.windows);
    const noMemory = windowsFromGrid(g);
    expect(gridFromWindows(noMemory)).toEqual(g);
    expect(noMemory).toEqual([
      { days: ["mon", "tue", "wed", "thu", "fri"], start: "00:30", end: "02:00" },
      { days: ["mon", "tue", "wed", "thu", "fri", "sat", "sun"], start: "02:00", end: "05:30" },
      { days: ["sat", "sun"], start: "05:30", end: "07:00" },
    ]);
  });

  it("keeps a window that still fits and adds only the new block", () => {
    const g = gridFromWindows(schedule.windows);
    g[2][24] = true; // wed 12:00-12:30
    g[2][25] = true; // wed 12:30-13:00
    const out = windowsFromGrid(g, schedule.windows);
    expect(out).toHaveLength(3);
    expect(out.filter((w) => w.days.length === 1)).toEqual([
      { days: ["wed"], start: "12:00", end: "13:00" },
    ]);
  });

  it("joins a block painted one cell at a time into one window", () => {
    const g = emptyGrid();
    g[2][24] = true;
    const first = windowsFromGrid(g, []);
    g[2][25] = true;
    const second = windowsFromGrid(g, first);
    expect(second).toEqual([{ days: ["wed"], start: "12:00", end: "13:00" }]);
  });

  it("drops a window that no longer fits", () => {
    const g = gridFromWindows(schedule.windows);
    g[0][1] = false; // erase the first half hour on monday
    const out = windowsFromGrid(g, schedule.windows);
    expect(out).not.toContainEqual(schedule.windows[0]);
    expect(gridFromWindows(out)).toEqual(g);
  });

  it("splits a staircase into one window per distinct day set", () => {
    const g = emptyGrid();
    g[0][2] = true; // mon 01:00-01:30
    g[0][3] = true; // mon 01:30-02:00
    g[1][3] = true; // tue 01:30-02:00
    expect(windowsFromGrid(g)).toEqual([
      { days: ["mon"], start: "01:00", end: "01:30" },
      { days: ["mon", "tue"], start: "01:30", end: "02:00" },
    ]);
  });

  it("writes a block that fills the day as a whole-day window", () => {
    const g = emptyGrid();
    for (let t = 0; t < 48; t++) g[5][t] = true; // saturday, all day
    expect(windowsFromGrid(g)).toEqual([{ days: ["sat"], start: "00:00", end: "00:00" }]);
  });

  it("renders a wrapping window as the tail of one day and the head of the next", () => {
    const g = gridFromWindows([{ days: ["fri"], start: "22:00", end: "06:00" }]);
    expect(g[4][44]).toBe(true); // friday 22:00
    expect(g[4][47]).toBe(true); // friday 23:30
    expect(g[5][0]).toBe(true); // saturday 00:00
    expect(g[5][11]).toBe(true); // saturday 05:30
    expect(g[5][12]).toBe(false); // saturday 06:00
  });

  it("treats midnight as the end of the day", () => {
    expect(windowsFromGrid(gridFromWindows([{ days: ["mon"], start: "22:00", end: "00:00" }]))).toEqual([
      { days: ["mon"], start: "22:00", end: "00:00" },
    ]);
  });

  it("has seven rows and forty-eight cells", () => {
    expect(DAYS).toHaveLength(7);
    expect(emptyGrid()[0]).toHaveLength(48);
  });
});

describe("describeWindow", () => {
  it("names a day", () =>
    expect(describeWindow({ days: ["fri"], start: "22:00", end: "06:00" })).toBe("on Fri, 22:00–06:00"));
  it("names the weekdays", () =>
    expect(describeWindow(schedule.windows[0])).toBe("weekdays, 00:30–05:30"));
  it("counts the hours across midnight", () =>
    expect(windowHours({ days: ["fri"], start: "22:00", end: "06:00" })).toBe(8));
});

describe("windowLabel", () => {
  it("says open with the spec", () =>
    expect(windowLabel({ spec: "00:30-05:30", open: true })).toBe("open · 00:30-05:30"));
  it("says shut with the next open", () =>
    expect(windowLabel({ spec: "00:30-05:30", open: false, next_at: "00:30" })).toBe(
      "shut · opens 00:30",
    ));
  it("survives a missing window", () => expect(windowLabel(null)).toBe(""));
});

describe("idleNote", () => {
  it("points at the node when the window is open", () =>
    expect(idleNote({ spec: "00:30-05:30", open: true })).toContain("start the node"));
  it("names the next open when the window is shut", () =>
    expect(idleNote({ spec: "00:30-05:30", open: false, next_at: "00:30" })).toContain(
      "Next window opens at 00:30",
    ));
});
