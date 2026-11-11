import { describe, expect, it } from "vitest";

import { resolveBack, PAGES } from "./nav";

describe("resolveBack", () => {
  it("uses the origin a link recorded", () =>
    expect(resolveBack({ from: { to: "/queue?state=failed", label: "Queue" } }, "/queue")).toEqual({
      to: "/queue?state=failed",
      label: "Queue",
    }));

  it("keeps the filter and the page in the destination", () =>
    expect(resolveBack({ from: { to: "/queue?decision=skip&page=3", label: "Queue" } }, "/queue").to).toBe(
      "/queue?decision=skip&page=3",
    ));

  it("falls back to the parent when the page was opened cold", () =>
    expect(resolveBack(null, "/runs")).toEqual({ to: "/runs", label: "Runs" }));

  it("ignores a half-written origin", () =>
    expect(resolveBack({ from: { label: "Queue" } }, "/queue")).toEqual({
      to: "/queue",
      label: PAGES["/queue"],
    }));

  it("refuses a destination that is not a path", () =>
    expect(resolveBack({ from: { to: "https://example.com", label: "Queue" } }, "/queue").to).toBe("/queue"));

  it("always names a page", () =>
    expect(resolveBack(undefined, "/").label).toBe("Overview"));
});
