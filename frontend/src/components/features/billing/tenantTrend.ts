// A tenant's recent spend as a trend line beside its row: the days of the
// trend's window, the quiet ones at zero, read the way every spend chart
// reads a series (spendStats). Pure, so the dashboard and any other list draw
// the same line from the same buckets.
import type { TimeBucket } from "@/gen/paladin/admin/v1/billing_service_pb";

import { STEP_MS, type Granularity } from "./spendSeries";
import { spendStats } from "./spendStats";

/** What a trend's buckets are: one per day. */
export const TREND_GRANULARITY: Granularity = "day";

/** How many days a trend looks back. */
export const TREND_DAYS = 30;

/** The trend's window: TREND_DAYS up to `now`. */
export function trendPeriod(now: Date): { start: Date; end: Date } {
  return {
    start: new Date(now.getTime() - TREND_DAYS * STEP_MS[TREND_GRANULARITY]),
    end: now,
  };
}

/**
 * The values to draw: every day of the window, a day without a charge at
 * zero. Nothing to draw — no charge in the window — is an empty list, so no
 * flat line claims a trend.
 */
export function trendValues(
  buckets: readonly TimeBucket[],
  period: { start: Date; end: Date },
): number[] {
  if (buckets.length === 0) return [];
  return spendStats(buckets, period, TREND_GRANULARITY).points.map(
    (p) => p.value,
  );
}
