// A tenant's live capabilities, every page of them as CapabilityService.List
// gives them: what the tenant's topology draws its principals from.
import { queryOptions } from "@tanstack/react-query";

import { CapabilityDisposition } from "@/gen/paladin/admin/v1/capability_service_pb";
import { capabilityClient } from "@/lib/connect/client";
import { listAllPages } from "@/lib/listAllPages";

/** Every tenant's live capabilities, whichever tenant. */
export const LIVE_CAPABILITIES_QUERY_KEY = ["liveCapabilities"] as const;

/** One tenant's live capabilities, by its id. */
export function liveCapabilitiesQuery(tenantId: string) {
  return queryOptions({
    queryKey: [...LIVE_CAPABILITIES_QUERY_KEY, tenantId] as const,
    enabled: tenantId !== "",
    queryFn: ({ signal }) =>
      listAllPages(async (pageToken) => {
        const res = await capabilityClient.list(
          {
            tenantId,
            dispositions: [CapabilityDisposition.ACTIVE],
            pageToken,
          },
          { signal },
        );
        return { items: res.capabilities, next: res.nextPageToken };
      }),
  });
}
