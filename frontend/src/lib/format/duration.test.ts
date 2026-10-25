import { describe, expect, it } from "vitest";

import { NO_DURATION, formatDuration, spanMs } from "./duration";

const HOUR_MS = 3_600_000;
const DAY_MS = 24 * HOUR_MS;

describe("formatDuration", () => {
  it.each([
    [0, "0 ms"],
    [850, "850 ms"],
    [12_000, "12 s"],
    [59_400, "59 s"],
    [60_000, "1 min"],
    [184_000, "3 min 4 s"],
    [HOUR_MS, "1 h"],
    [7_500_000, "2 h 5 min"],
    [23 * HOUR_MS + 59 * 60_000, "23 h 59 min"],
    [DAY_MS, "1 d"],
    [7 * DAY_MS + 2 * HOUR_MS, "7 d 2 h"],
    [90 * DAY_MS, "90 d"],
    [-1, NO_DURATION],
    [Number.NaN, NO_DURATION],
  ])("%s ms reads %j", (ms, want) => {
    expect(formatDuration(ms)).toBe(want);
  });
});

describe("spanMs", () => {
  const at = (seconds: number, nanos = 0) => ({
    seconds: BigInt(seconds),
    nanos,
  });

  it("measures from start to end, nanos included", () => {
    expect(spanMs(at(100), at(160))).toBe(60_000);
    expect(spanMs(at(100), at(100, 500_000_000))).toBe(500);
  });

  it("measures up to a Date", () => {
    expect(spanMs(at(100), new Date(160_000))).toBe(60_000);
  });

  it("is zero for the same instant", () => {
    expect(spanMs(at(100), at(100))).toBe(0);
  });

  it.each([
    ["no start", undefined, at(100)],
    ["no end", at(100), undefined],
    ["an end before the start", at(160), at(100)],
  ])("is null with %s", (_, start, end) => {
    expect(spanMs(start, end)).toBeNull();
  });
});
