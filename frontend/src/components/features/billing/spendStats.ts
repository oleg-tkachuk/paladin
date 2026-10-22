// What a spend series says, beside its chart: the points to draw over the
// whole period, its busiest bucket, and the average per bucket. Pure, so a
// tenant's billing and the platform's read a series the same way.
import { timestampDate } from "@bufbuild/protobuf/wkt";

import type { TimeBucket } from "@/gen/paladin/admin/v1/billing_service_pb";
import type { Money } from "@/gen/google/type/money_pb";
import { moneyToNanos, moneyToNumber } from "@/lib/format/money";

import { fillSeries, type Granularity, type SpendPoint } from "./spendSeries";

export interface SpendStats {
  /** Every bucket of the period, the quiet ones at zero. */
  points: SpendPoint[];
  /** The bucket that spent the most; null for a series with none. */
  peak: TimeBucket | null;
  /** The period's spend over every one of its buckets, quiet ones too. */
  avgNanos: bigint;
  /** The series' unit, "" when no bucket names one. */
  unit: string;
}

export function spendStats(
  buckets: readonly TimeBucket[],
  period: { start: Date; end: Date } | undefined,
  granularity: Granularity,
): SpendStats {
  const raw = buckets.flatMap((b) =>
    b.start
      ? [
          {
            start: timestampDate(b.start),
            value: moneyToNumber(b.spent),
            chargeCount: Number(b.chargeCount),
            spent: b.spent,
          },
        ]
      : [],
  );
  // The axis covers the whole period, so a quiet day reads as a gap.
  const points = period
    ? fillSeries(raw, period.start, period.end, granularity)
    : raw;
  const peak = buckets.reduce<TimeBucket | null>(
    (acc, b) =>
      !acc || moneyToNanos(b.spent) > moneyToNanos(acc.spent) ? b : acc,
    null,
  );
  const sum = buckets.reduce((acc, b) => acc + moneyToNanos(b.spent), 0n);
  const avgNanos = points.length === 0 ? 0n : sum / BigInt(points.length);
  const unit =
    buckets.find((b) => b.spent?.currencyCode)?.spent?.currencyCode ?? "";
  return { points, peak, avgNanos, unit };
}

/** The spend of a point the chart hovers, as the server sent it. */
export const pointSpent = (p: SpendPoint) => p.spent as Money | undefined;
