import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { TimeBucketSchema } from "@/gen/paladin/admin/v1/billing_service_pb";

import { TREND_DAYS, trendPeriod, trendValues } from "./tenantTrend";

const day = (d: number) => new Date(Date.UTC(2026, 9, d));
const bucket = (d: number, units: bigint) =>
  create(TimeBucketSchema, {
    start: timestampFromDate(day(d)),
    chargeCount: 1n,
    spent: { currencyCode: "USD", units, nanos: 0 },
  });

describe("tenantTrend", () => {
  it("looks back TREND_DAYS from now", () => {
    const now = day(31);
    const { start, end } = trendPeriod(now);
    expect(end).toBe(now);
    expect((now.getTime() - start.getTime()) / 86_400_000).toBe(TREND_DAYS);
  });

  it("draws every day of the window, the quiet ones at zero", () => {
    expect(
      trendValues([bucket(2, 3n), bucket(4, 1n)], {
        start: day(1),
        end: day(5),
      }),
    ).toEqual([0, 3, 0, 1, 0]);
  });

  it("draws nothing for a tenant that spent nothing", () => {
    expect(trendValues([], { start: day(1), end: day(5) })).toEqual([]);
  });
});
