// Billing reads, as the console asks for them: one key per question, so a
// list and the page that drills into it share what they already read.
import { queryOptions } from "@tanstack/react-query";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

import { billingClient } from "@/lib/connect/client";
import { normalizeError } from "@/lib/connect/error";

/** Every billing read. */
export const BILLING_QUERY_KEY = ["billing"] as const;

/** A period the ledger is summed over. */
export interface BillingPeriod {
  periodStart: Timestamp;
  periodEnd: Timestamp;
}

/** The instant a timestamp names, in milliseconds, for a query key. */
function millis(t: Timestamp): number {
  return Number(t.seconds) * 1000 + Math.floor(t.nanos / 1_000_000);
}

/**
 * Every tenant that spent over the period, the most first — the platform
 * admin's view; anyone else is refused.
 */
export function tenantSpendQuery(period: BillingPeriod) {
  return queryOptions({
    queryKey: [
      ...BILLING_QUERY_KEY,
      "tenant-spend",
      millis(period.periodStart),
      millis(period.periodEnd),
    ] as const,
    queryFn: async ({ signal }) => {
      try {
        return await billingClient.listTenantSpend(period, { signal });
      } catch (err) {
        throw normalizeError(err);
      }
    },
  });
}

/**
 * What every tenant was charged over the period, a bucket at a time — the
 * platform admin's view; anyone else is refused.
 */
export function platformSeriesQuery(
  period: BillingPeriod,
  granularity: string,
) {
  return queryOptions({
    queryKey: [
      ...BILLING_QUERY_KEY,
      "platform-series",
      millis(period.periodStart),
      millis(period.periodEnd),
      granularity,
    ] as const,
    queryFn: async ({ signal }) => {
      try {
        return await billingClient.getPlatformTimeSeries(
          { ...period, granularity },
          { signal },
        );
      } catch (err) {
        throw normalizeError(err);
      }
    },
  });
}
