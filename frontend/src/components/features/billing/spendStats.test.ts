import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { TimeBucketSchema } from "@/gen/paladin/admin/v1/billing_service_pb";

import { spendStats } from "./spendStats";

const NANOS = 1_000_000_000n;
const day = (d: number) => new Date(Date.UTC(2026, 9, d));
const bucket = (d: number, units: bigint, charges: bigint) =>
  create(TimeBucketSchema, {
    start: timestampFromDate(day(d)),
    chargeCount: charges,
    spent: { currencyCode: "USD", units, nanos: 0 },
  });

describe("spendStats", () => {
  it("fills the period, finds the peak and averages over every bucket", () => {
    const s = spendStats(
      [bucket(2, 6n, 3n), bucket(4, 2n, 1n)],
      { start: day(1), end: day(4) },
      "day",
    );
    expect(s.points.map((p) => p.value)).toEqual([0, 6, 0, 2]);
    expect(s.peak?.spent?.units).toBe(6n);
    // 8 over the four days of the period, the quiet ones too.
    expect(s.avgNanos).toBe((8n * NANOS) / 4n);
    expect(s.unit).toBe("USD");
  });

  it("says nothing of a series with no charge", () => {
    const s = spendStats([], { start: day(1), end: day(2) }, "day");
    expect(s.peak).toBeNull();
    expect(s.avgNanos).toBe(0n);
    expect(s.unit).toBe("");
    expect(s.points).toHaveLength(2);
  });
});
