// Every tenant's capabilities together, as CapabilityService.List pages them
// with no tenant, newest first: the platform's index, for a caller spanning
// tenants.
import { infiniteQueryOptions } from "@tanstack/react-query";

import type { CapabilityDisposition } from "@/gen/paladin/admin/v1/capability_service_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import { capabilityClient } from "@/lib/connect/client";

/** Every tenant's capabilities, whatever the filter. */
export const PLATFORM_CAPABILITIES_QUERY_KEY = [
  "platformCapabilities",
] as const;

/** Capabilities one page shows. */
export const PLATFORM_CAPABILITIES_PAGE_SIZE = 100;

/** Every tenant's capabilities in these dispositions, a page at a time. */
export function platformCapabilitiesQuery(
  dispositions: readonly CapabilityDisposition[],
) {
  return infiniteQueryOptions({
    queryKey: [...PLATFORM_CAPABILITIES_QUERY_KEY, [...dispositions]] as const,
    retry: false,
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      capabilityClient.list(
        {
          tenantId: "",
          dispositions: [...dispositions],
          sortOrder: SortOrder.DESC,
          pageSize: PLATFORM_CAPABILITIES_PAGE_SIZE,
          pageToken: pageParam,
        },
        { signal },
      ),
    getNextPageParam: (last) => last.nextPageToken || undefined,
  });
}
