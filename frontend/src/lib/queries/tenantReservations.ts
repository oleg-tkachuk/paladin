// The holds on a tenant's budget, as TenantBudgetService.ListReservations
// pages them: one key per tenant, so the Budget tab's Refresh reads them
// again with the budget.
import { infiniteQueryOptions, type QueryClient } from "@tanstack/react-query";

import { tenantBudgetClient } from "@/lib/connect/client";

/** Every tenant's holds, whichever tenant. */
export const TENANT_RESERVATIONS_QUERY_KEY = ["tenantReservations"] as const;

/** Holds one page shows. */
export const TENANT_RESERVATIONS_PAGE_SIZE = 50;

/** One tenant's holds, by its id. */
export const tenantReservationsKey = (tenantId: string) =>
  [...TENANT_RESERVATIONS_QUERY_KEY, tenantId] as const;

/** A tenant's holds, soonest to expire first, a page at a time. */
export function tenantReservationsQuery(tenantId: string) {
  return infiniteQueryOptions({
    queryKey: tenantReservationsKey(tenantId),
    enabled: tenantId !== "",
    retry: false,
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      tenantBudgetClient.listReservations(
        {
          tenantId,
          pageSize: TENANT_RESERVATIONS_PAGE_SIZE,
          pageToken: pageParam,
        },
        { signal },
      ),
    getNextPageParam: (last) => last.nextPageToken || undefined,
  });
}

/** Reads a tenant's holds again. */
export function invalidateTenantReservations(
  qc: QueryClient,
  tenantId: string,
): Promise<void> {
  return qc.invalidateQueries({ queryKey: tenantReservationsKey(tenantId) });
}
