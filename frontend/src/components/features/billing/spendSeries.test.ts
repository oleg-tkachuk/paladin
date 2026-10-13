import { describe, expect, it } from "vitest";

import { bucketStart, fillSeries, niceMax } from "./spendSeries";

const utc = (iso: string) => new Date(iso);

describe("bucketStart", () => {
  it("truncates as the server's date_trunc does, in UTC", () => {
    const t = utc("2026-10-08T17:42:10Z"); // a Thursday
    expect(bucketStart(t, "hour").toISOString()).toBe(
      "2026-10-08T17:00:00.000Z",
    );
    expect(bucketStart(t, "day").toISOString()).toBe(
      "2026-10-08T00:00:00.000Z",
    );
    expect(bucketStart(t, "week").toISOString()).toBe(
      "2026-10-05T00:00:00.000Z",
    );
  });

  it("starts a Sunday's week on the Monday before it", () => {
    expect(bucketStart(utc("2026-10-11T10:00:00Z"), "week").toISOString()).toBe(
      "2026-10-05T00:00:00.000Z",
    );
  });
});

describe("fillSeries", () => {
  it("fills the days with no charge at zero, in order", () => {
    const filled = fillSeries(
      [{ start: utc("2026-10-03T00:00:00Z"), value: 2, chargeCount: 1 }],
      utc("2026-10-01T09:00:00Z"),
      utc("2026-10-04T23:00:00Z"),
      "day",
    );
    expect(
      filled.map((p) => [p.start.toISOString().slice(0, 10), p.value]),
    ).toEqual([
      ["2026-10-01", 0],
      ["2026-10-02", 0],
      ["2026-10-03", 2],
      ["2026-10-04", 0],
    ]);
  });

  it("keeps a point the range does not cover", () => {
    const outside = {
      start: utc("2026-09-20T00:00:00Z"),
      value: 5,
      chargeCount: 2,
    };
    const filled = fillSeries(
      [outside],
      utc("2026-10-01T00:00:00Z"),
      utc("2026-10-02T00:00:00Z"),
      "day",
    );
    expect(filled[0]).toBe(outside);
    expect(filled).toHaveLength(3);
  });
});

describe("niceMax", () => {
  it.each([
    [0, 1],
    [0.0034, 0.005],
    [0.024, 0.05],
    [7, 10],
    [120, 200],
    [1000, 1000],
  ])("rounds %s up to %s", (max, want) => {
    expect(niceMax(max)).toBeCloseTo(want);
  });
});
