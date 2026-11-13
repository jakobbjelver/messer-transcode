import { describe, expect, it } from "vitest";

import { columnsFor, queueHref, TABS, tabByKey, tabFrom } from "./queue";

const counts = {
  queued: 747,
  encode: 739,
  remux: 8,
  inflight: 1,
  failed: 7,
  done: 3136,
  skipped: 402,
  gone: 1465,
  everything: 4293,
};

describe("tabs", () => {
  it("counts what each tab means", () => {
    const byKey = Object.fromEntries(TABS.map((t) => [t.key, t.count(counts)]));
    expect(byKey.queued).toBe(747);
    expect(byKey.done).toBe(3136);
    expect(byKey.gone).toBe(1465);
    expect(byKey.everything).toBe(4293);
  });

  it("gives retired files their own filter rather than burying them in All", () => {
    expect(tabByKey("gone").query).toEqual({ state: "gone" });
  });

  it("falls back to Queued for an unknown or missing tab", () => {
    expect(tabByKey(null).key).toBe("queued");
    expect(tabByKey("nonsense").key).toBe("queued");
  });

  it("reads the tab back out of URL parameters", () => {
    expect(tabFrom(new URLSearchParams("tab=done")).key).toBe("done");
    expect(tabFrom(new URLSearchParams("")).key).toBe("queued");
    expect(tabFrom(new URLSearchParams("tab=failed")).key).toBe("failed");
  });
});

describe("columnsFor", () => {
  it("shows a finished row's savings and quality", () =>
    expect(columnsFor("done")).toEqual(["file", "state", "finished", "saved", "vmaf", "size"]));

  it("shows source and target for work that has not run", () =>
    expect(columnsFor("queued")).toContain("source"));

  it("drops the bitrate columns for a skipped row", () =>
    expect(columnsFor("skipped")).toEqual(["file", "state", "why", "size"]));
});

describe("queueHref", () => {
  it("omits the default tab, the first page and an empty search", () =>
    expect(queueHref("queued", 1, "")).toBe("/queue"));

  it("keeps the tab, the page and the search", () =>
    expect(queueHref("done", 3, "alien")).toBe("/queue?tab=done&page=3&q=alien"));
});
