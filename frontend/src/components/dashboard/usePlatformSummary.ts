"use client";

import { useQuery } from "@tanstack/react-query";

import { adminSystemClient } from "@/lib/connect/client";

/** One tenant row is enough: the dashboard reads the fleet totals. */
const SUMMARY_TENANT_ROWS = 1;

/** The platform's counts, once, for the dashboard's checklist and quotas. */
export function usePlatformSummary() {
  return useQuery({
    queryKey: ["dashboard", "platformSummary"],
    queryFn: ({ signal }) =>
      adminSystemClient.getPlatformStats(
        { tenantPage: { pageSize: SUMMARY_TENANT_ROWS, pageToken: "" } },
        { signal },
      ),
    retry: false,
  });
}
