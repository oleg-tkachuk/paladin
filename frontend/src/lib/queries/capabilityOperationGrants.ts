// What each capability operation unlocks without a tenant policy, as
// CapabilityService.ListOperationGrants serves the built-in Cedar policy's
// table (ADR-0038). Read once for every page that shows a capability's ops.
import { queryOptions } from "@tanstack/react-query";

import { capabilityClient } from "@/lib/connect/client";

/**
 * Its own root, not under the capability records' key: a revoke invalidates
 * those, and this table does not change with any capability.
 */
export const CAPABILITY_OPERATION_GRANTS_QUERY_KEY = [
  "capabilityOperationGrants",
] as const;

export function capabilityOperationGrantsQuery() {
  return queryOptions({
    queryKey: CAPABILITY_OPERATION_GRANTS_QUERY_KEY,
    queryFn: async ({ signal }) =>
      (await capabilityClient.listOperationGrants({}, { signal })).grants,
    // The table is the server binary's built-in policy: it changes with a
    // deploy, not while a tab is open.
    staleTime: Infinity,
  });
}
