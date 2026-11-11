import { describe, expect, it } from "vitest";

import { byGroup, deletePath, getPath, listText, parseList, setPath, showValue, summaryLine } from "./config";
import type { Diff } from "./types";

const doc = {
  quality: { table: [{ kbps: 6000 }, { kbps: 2700 }], mode: "table" },
  audio: { keep_languages: ["swe", "eng"] },
};

describe("getPath", () => {
  it("reads a nested value", () => expect(getPath(doc, "/quality/table/1/kbps")).toBe(2700));
  it("reads through an array index", () => expect(getPath(doc, "quality/table/0/kbps")).toBe(6000));
  it("is undefined for a missing branch", () => expect(getPath(doc, "/quality/nope/deep")).toBeUndefined());
});

describe("setPath", () => {
  it("replaces a leaf", () => {
    const next = setPath(doc, "/quality/table/0/kbps", 6200);
    expect(getPath(next, "/quality/table/0/kbps")).toBe(6200);
  });

  it("does not mutate the input", () => {
    const next = setPath(doc, "/quality/mode", "probe");
    expect(doc.quality.mode).toBe("table");
    expect(next.quality.mode).toBe("probe");
  });

  it("keeps untouched branches identical", () => {
    const next = setPath(doc, "/quality/mode", "probe");
    expect(next.audio).toBe(doc.audio);
  });

  it("creates a missing branch of the implied type", () => {
    const next = setPath(doc, "/subtitles/keep_languages/0", "eng");
    expect(getPath(next, "/subtitles/keep_languages/0")).toBe("eng");
  });
});

describe("deletePath", () => {
  it("removes a key", () => {
    const next = deletePath(doc, "/audio/keep_languages");
    expect("keep_languages" in next.audio).toBe(false);
  });
  it("removes an array element", () => {
    const next = deletePath(doc, "/quality/table/0");
    expect(next.quality.table).toHaveLength(1);
    expect(next.quality.table[0].kbps).toBe(2700);
  });
});

describe("showValue", () => {
  it("names the absent case", () => expect(showValue(undefined)).toBe("not set"));
  it("reads a boolean as a switch", () => expect(showValue(false)).toBe("off"));
  it("joins a list", () => expect(showValue(["swe", "eng"])).toBe("swe, eng"));
  it("does not print an empty string as nothing", () => expect(showValue("")).toBe("(empty)"));
});

describe("list helpers", () => {
  it("round-trips a comma list", () => {
    expect(parseList(" swe, eng ,, ")).toEqual(["swe", "eng"]);
    expect(listText(["swe", "eng"])).toBe("swe, eng");
    expect(listText(undefined)).toBe("");
  });
});

describe("summaryLine", () => {
  it("counts what changed", () => {
    const d = {
      summary: { added: 1, removed: 0, changed: 2, reordered: 0 },
      groups: [],
      changes: [],
      text: "",
    } as Diff;
    expect(summaryLine(d)).toBe("2 changes · 1 addition");
  });
  it("says so when nothing does", () => {
    const d = { summary: { added: 0, removed: 0, changed: 0, reordered: 0 } } as Diff;
    expect(summaryLine(d)).toBe("nothing changes");
  });
});

describe("byGroup", () => {
  it("groups in first-seen order", () => {
    const changes = [
      { path: "/quality/a", kind: "change" as const, label: "a", group: "quality" },
      { path: "/safety/b", kind: "change" as const, label: "b", group: "safety" },
      { path: "/quality/c", kind: "change" as const, label: "c", group: "quality" },
    ];
    const g = byGroup(changes);
    expect(g.map((x) => x.key)).toEqual(["quality", "safety"]);
    expect(g[0].changes).toHaveLength(2);
  });
});
