import { describe, expect, it } from "vitest";

import { formatDuration } from "./duration";

describe("formatDuration", () => {
  it.each([
    [0, "0 ms"],
    [850, "850 ms"],
    [12_000, "12 s"],
    [59_400, "59 s"],
    [60_000, "1 min"],
    [184_000, "3 min 4 s"],
    [3_600_000, "1 h"],
    [7_500_000, "2 h 5 min"],
    [-1, "—"],
    [Number.NaN, "—"],
  ])("%s ms reads %j", (ms, want) => {
    expect(formatDuration(ms)).toBe(want);
  });
});
