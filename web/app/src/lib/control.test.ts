import { describe, expect, it } from "vitest";

import { actionNote, controlSummary } from "./control";
import type { ControlResponse } from "./types";

const base: ControlResponse = {
  control: { paused: false, override: "" },
  window: { open: true, spec: "00:30-05:30" },
  claiming: { allowed: true, reason: "" },
  now: 1_800_000_000,
};

describe("controlSummary", () => {
  it("reports the schedule when nothing is steering", () =>
    expect(controlSummary(base, false)).toEqual({
      tone: "ok",
      label: "open",
      detail: "schedule 00:30-05:30",
    }));

  it("says when the next window opens", () =>
    expect(
      controlSummary({ ...base, window: { open: false, spec: "00:30-05:30", next_at: "00:30" } }, false),
    ).toEqual({ tone: "ok", label: "shut", detail: "opens 00:30" }));

  it("puts the pause above the schedule", () => {
    const s = controlSummary(
      {
        ...base,
        control: { paused: true, paused_at: base.now - 8_040, paused_by: "web", override: "" },
      },
      true,
    );
    expect(s.label).toBe("paused");
    expect(s.detail).toContain("2 h 14 m");
    expect(s.detail).toContain("one file is still encoding");
  });

  it("reports a forced window with its deadline", () => {
    const s = controlSummary(
      { ...base, control: { paused: false, override: "open", override_until: base.now + 3600 } },
      false,
    );
    expect(s.label).toBe("forced open");
    expect(s.detail).toContain("then the schedule");
  });

  it("says a forced shut starts nothing", () =>
    expect(controlSummary({ ...base, control: { paused: false, override: "close" } }, false)).toEqual({
      tone: "forced",
      label: "forced shut",
      detail: "no new claims start",
    }));

  it("survives a missing response", () =>
    expect(controlSummary(null, false).label).toBe("reading"));
});

describe("actionNote", () => {
  it("counts what moved", () => expect(actionNote({ applied: 3, refused: [] })).toBe("3 files"));
  it("singularises", () => expect(actionNote({ applied: 1, refused: [] })).toBe("1 file"));
  it("names the first refusal", () =>
    expect(actionNote({ applied: 2, refused: [{ id: 1, state: "done" }] })).toBe("2 files · 1 refused (in done)"));
  it("says nothing happened", () => expect(actionNote({ applied: 0, refused: [] })).toBe("nothing to do"));
});
