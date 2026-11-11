import { describe, expect, it } from "vitest";

import { idleNote, scheduleLabel, windowLabel } from "./schedule";

const schedule = {
  weekday: "00:30-05:30",
  weekend: "02:00-07:00",
  drain_before_minutes: 15,
  timezone: "Europe/Stockholm",
};

describe("scheduleLabel", () => {
  it("states both windows, the zone and the drain", () =>
    expect(scheduleLabel(schedule)).toBe(
      "weekdays 00:30-05:30 · weekends 02:00-07:00 · Europe/Stockholm · drain 15m",
    ));
  it("drops the drain when there is none", () =>
    expect(scheduleLabel({ ...schedule, drain_before_minutes: 0 })).not.toContain("drain"));
  it("falls back to UTC when the zone is empty", () =>
    expect(scheduleLabel({ ...schedule, timezone: "" })).toContain("UTC"));
  it("renders nothing without a schedule", () => expect(scheduleLabel(null)).toBe(""));
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
