// The platform-wide reads that tell which tenants are near or at their
// limits, each followed to its last page: a set cut at one page would call a
// tenant past it within its limits. Platform-admin reads; anyone else is
// refused, and the caller shows its tenants without limit states.
import { queryOptions } from "@tanstack/react-query";

import { API_PAGE_SIZE_MAX } from "@/constants";
import { PlatformStatsSignal } from "@/gen/paladin/admin/v1/system_service_pb";
import type { TenantBudgetSummary } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import { adminSystemClient, tenantBudgetClient } from "@/lib/connect/client";
import { MAX_LIST_PAGES } from "./paging";

/** Every tenant-limits read. */
export const TENANT_LIMITS_QUERY_KEY = ["tenantLimits"] as const;

// Summarize's limit is validated to the console's API page maximum (500).
const SUMMARIZE_LIMIT_MAX = API_PAGE_SIZE_MAX;

/** The ids of every tenant listed under a quota signal. */
async function signalTenantIds(
  signal: PlatformStatsSignal,
  abort?: AbortSignal,
): Promise<string[]> {
  const acc: string[] = [];
  let token = "";
  let pages = 0;
  do {
    const res = await adminSystemClient.listPlatformStatsTenants(
      { signal, page: { pageSize: API_PAGE_SIZE_MAX, pageToken: token } },
      { signal: abort },
    );
    acc.push(...res.tenants.map((t) => t.tenantId));
    token = res.page?.nextPageToken ?? "";
    pages += 1;
  } while (token && pages < MAX_LIST_PAGES);
  return acc;
}

/** The active tenants whose budget utilisation is at or past `thresholdPct`. */
async function budgetSummaries(
  thresholdPct: number,
  abort?: AbortSignal,
): Promise<TenantBudgetSummary[]> {
  const acc: TenantBudgetSummary[] = [];
  let token = "";
  let pages = 0;
  do {
    const res = await tenantBudgetClient.summarize(
      {
        thresholdPct,
        unlimitedOnly: false,
        excludeInactive: true,
        limit: SUMMARIZE_LIMIT_MAX,
        pageToken: token,
      },
      { signal: abort },
    );
    acc.push(...res.summaries);
    token = res.nextPageToken;
    pages += 1;
  } while (token && pages < MAX_LIST_PAGES);
  return acc;
}

/** The quota signals the tenant limits read, each its own query. */
export const QUOTA_LIMIT_SIGNALS = {
  atLimit: PlatformStatsSignal.QUOTA_AT_LIMIT,
  nearLimit: PlatformStatsSignal.QUOTA_NEAR_LIMIT,
} as const;

export function quotaSignalTenantsQuery(signal: PlatformStatsSignal) {
  return queryOptions({
    queryKey: [...TENANT_LIMITS_QUERY_KEY, "quota", signal] as const,
    queryFn: ({ signal: abort }) => signalTenantIds(signal, abort),
  });
}

export function budgetAlertsQuery(thresholdPct: number) {
  return queryOptions({
    queryKey: [...TENANT_LIMITS_QUERY_KEY, "budget", thresholdPct] as const,
    queryFn: ({ signal: abort }) => budgetSummaries(thresholdPct, abort),
  });
}
