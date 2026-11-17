"use client";

import { useCallback } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import { tenantClient } from "@/lib/connect/client";
import { errorMessage } from "@/hooks/errorContract";
import { retiredSlugsQuery, TENANTS_QUERY_KEY } from "@/lib/queries/tenants";

/** The resource-name prefix of a retired slug, as ReleaseTenantSlug takes it. */
export const RETIRED_SLUG_NAME_PREFIX = "retiredTenantSlugs/";

/**
 * The slugs purged tenants held, whether a new tenant is refused them, and
 * the release of one. A release throws on refusal, for the caller to report;
 * either way every tenants list reads again, as after any tenant change.
 */
export function useRetiredTenantSlugs() {
  const qc = useQueryClient();
  const query = useQuery(retiredSlugsQuery());
  const releaseSlug = useCallback(
    async (slug: string): Promise<void> => {
      try {
        await tenantClient.releaseTenantSlug({
          name: RETIRED_SLUG_NAME_PREFIX + slug,
        });
      } finally {
        void qc.invalidateQueries({ queryKey: TENANTS_QUERY_KEY });
      }
    },
    [qc],
  );
  return {
    slugs: query.data?.slugs ?? [],
    enforced: query.data?.enforced ?? true,
    loading: query.isPending,
    error: query.error
      ? errorMessage(query.error, "Failed to fetch retired slugs")
      : null,
    releaseSlug,
  };
}
