"use client";

import { useQuery } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";

import { useAuth } from "@/context/AuthContext";
import { objectTagClient } from "@/lib/connect/client";

/**
 * useDistinctTags fetches the whole-Collection distinct tag key→values that back
 * the tag-facet filter, via ObjectTagService.ListDistinctTags. It returns a
 * sorted `"key=value"` option list — the shape ObjectsFilterBar expects.
 *
 * The list is authoritative across the tenant's objects (not just the loaded
 * page). On error, or while the tenant / collection is unresolved, it returns an
 * empty list and the page falls back to client-side accumulation from the
 * objects already loaded — so the dropdown degrades gracefully.
 */
export function useDistinctTags(collection: string): string[] {
  const { user } = useAuth();
  const tenantId = user?.tenantId ?? "";

  const { data } = useQuery({
    queryKey: ["distinctTags", tenantId, collection],
    enabled: !!tenantId && !!collection,
    queryFn: async () => {
      const parent = `tenants/${tenantId}/collections/${collection}`;
      try {
        const res = await objectTagClient.listDistinctTags({ parent });
        const pairs: string[] = [];
        for (const [key, tv] of Object.entries(res.tags)) {
          for (const value of tv.values) pairs.push(`${key}=${value}`);
        }
        pairs.sort();
        return pairs;
      } catch (e: unknown) {
        // Non-fatal: the dropdown still works off client-side accumulation.
        console.debug("listDistinctTags failed", ConnectError.from(e).message);
        return [] as string[];
      }
    },
  });

  return data ?? [];
}
