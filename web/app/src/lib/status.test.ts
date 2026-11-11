import { describe, expect, it } from "vitest";

import { isRunning, statePresentation } from "./status";

describe("statePresentation", () => {
  it("maps done to success", () => expect(statePresentation("done")).toEqual({ status: "success", label: "Done" }));
  it("maps failed to danger", () => expect(statePresentation("failed").status).toBe("danger"));
  it("reads skipped in the human voice", () =>
    expect(statePresentation("skipped")).toEqual({ status: "neutral", label: "Left alone" }));
  it("keeps the three in-flight stages neutral with their own label", () => {
    for (const [stage, label] of [
      ["encoding", "Encoding"],
      ["verifying", "Verifying"],
      ["replacing", "Replacing"],
    ] as const) {
      // Neutral, not warning: a running job is not a problem, and an amber
      // triangle would say it is. The label and the moving bar carry "live".
      expect(statePresentation(stage)).toEqual({ status: "neutral", label });
    }
  });
  it("falls back to the raw state for something unknown", () =>
    expect(statePresentation("quarantined")).toEqual({ status: "neutral", label: "quarantined" }));
  it("never renders an empty label", () =>
    expect(statePresentation("").label).toBe("Unknown"));
});

describe("isRunning", () => {
  it("covers every stage that owns the machine", () => {
    expect(["encoding", "verifying", "replacing"].every(isRunning)).toBe(true);
    expect(isRunning("done")).toBe(false);
    expect(isRunning("planned")).toBe(false);
  });
});
