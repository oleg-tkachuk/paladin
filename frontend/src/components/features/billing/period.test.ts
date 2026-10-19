import { describe, expect, it } from "vitest";

import {
  MAX_CHART_BARS,
  allowedGranularities,
  isPeriod,
  pickGranularity,
  resolvePeriod,
  type Period,
} from "./period";

const NOW = new Date(Date.UTC(2026, 9, 19, 12, 0));
const DAY = 24 * 60 * 60 * 1000;

const period = (days: number): Period => ({
  start: new Date(NOW.getTime() - days * DAY),
  end: NOW,
});

describe("resolvePeriod", () => {
  it("reads a preset back from now", () => {
    expect(resolvePeriod("7d", "", "", NOW)).toEqual(period(7));
    expect(resolvePeriod("90d", "", "", NOW)).toEqual(period(90));
  });

  it("reads a custom range as whole UTC days, both ends included", () => {
    expect(resolvePeriod("custom", "2026-10-01", "2026-10-02", NOW)).toEqual({
      start: new Date("2026-10-01T00:00:00Z"),
      end: new Date("2026-10-03T00:00:00Z"),
    });
  });

  it("ends a range that reaches into the future now", () => {
    const p = resolvePeriod("custom", "2026-10-01", "2026-12-31", NOW);
    expect(isPeriod(p) && p.end).toEqual(NOW);
  });

  it.each([
    ["", "2026-10-02", "Choose both dates."],
    ["2026-10-05", "2026-10-02", "The start is after the end."],
    ["2026-11-01", "2026-11-02", "The period has not started yet."],
    ["2026-13-45", "2026-10-02", "Not a date."],
  ])("refuses %j – %j", (start, end, error) => {
    expect(resolvePeriod("custom", start, end, NOW)).toEqual({ error });
  });
});

describe("allowedGranularities", () => {
  it.each([
    [7, ["hour", "day", "week"]],
    [30, ["day", "week"]],
    [90, ["day", "week"]],
    [730, ["week"]],
  ])("%d days → %j", (days, want) => {
    expect(allowedGranularities(period(days))).toEqual(want);
  });

  it("allows a granularity at exactly the bar limit", () => {
    const hours = MAX_CHART_BARS;
    const p = {
      start: new Date(NOW.getTime() - hours * 60 * 60 * 1000),
      end: NOW,
    };
    expect(allowedGranularities(p)).toContain("hour");
  });
});

describe("pickGranularity", () => {
  it("keeps the wanted one while it fits, else takes the finest that does", () => {
    expect(pickGranularity(period(7), "hour")).toBe("hour");
    expect(pickGranularity(period(30), "hour")).toBe("day");
    expect(pickGranularity(period(730), "day")).toBe("week");
  });
});
