import { describe, expect, it } from "vitest";

import { bytes, clock, day, duration, fileName, gb, kbps, pct, timeOfDay } from "./format";

describe("bytes", () => {
  it("uses two decimals above a terabyte", () => expect(bytes(1.5e12)).toBe("1.50 TB"));
  it("uses two decimals for gigabytes", () => expect(bytes(8.42e9)).toBe("8.42 GB"));
  it("uses one decimal for megabytes", () => expect(bytes(2.1e6)).toBe("2.1 MB"));
  it("drops decimals for kilobytes", () => expect(bytes(1200)).toBe("1 KB"));
  it("floors at plain bytes", () => expect(bytes(999)).toBe("999 B"));
  it("never prints NaN", () => expect(bytes(Number.NaN)).toBe("—"));
});

describe("gb", () => {
  it("is one decimal of the gigabyte figure", () => expect(gb(454.62e9)).toBe("454.6"));
  it("treats zero as zero", () => expect(gb(0)).toBe("0.0"));
});

describe("duration", () => {
  it("formats hours and minutes", () => expect(duration(3725)).toBe("1h 02m"));
  it("formats minutes and seconds", () => expect(duration(125)).toBe("2m 05s"));
  it("formats seconds", () => expect(duration(42)).toBe("42s"));
  it("never prints a negative", () => expect(duration(-1)).toBe("—"));
});

describe("kbps", () => {
  it("groups thousands", () => expect(kbps(8500)).toBe("8,500 kbps"));
  it("shows a dash for zero", () => expect(kbps(0)).toBe("—"));
});

describe("pct", () => {
  it("rounds by default", () => expect(pct(38.4)).toBe("38%"));
  it("keeps a requested decimal", () => expect(pct(38.44, 1)).toBe("38.4%"));
});

describe("clock", () => {
  it("shows a dash for a zero timestamp", () => expect(clock(0)).toBe("—"));
  it("renders HH:MM in en-GB", () => expect(clock(Date.UTC(2026, 10, 10, 12, 5) / 1000)).toMatch(/^\d{2}:\d{2}$/));
});

describe("day and timeOfDay", () => {
  const iso = new Date(Date.UTC(2026, 10, 10, 3, 14)).toISOString();
  it("reads like a log line", () => expect(day(iso)).toMatch(/^\w{3} \d{2} \w{3}$/));
  it("renders a time of day", () => expect(timeOfDay(iso)).toMatch(/^\d{2}:\d{2}$/));
  it("does not throw on a bad date", () => expect(day("not a date")).toBe("—"));
});

describe("fileName", () => {
  it("takes the last segment", () => expect(fileName("/media/Film One/one.mkv")).toBe("one.mkv"));
  it("returns the input when there is no slash", () => expect(fileName("one.mkv")).toBe("one.mkv"));
});
