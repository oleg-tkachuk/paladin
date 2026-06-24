"use client";

import { useEffect, useState } from "react";

import { ConnectError } from "@connectrpc/connect";

import { useAuth } from "@/context/AuthContext";
import { objectTagClient } from "@/lib/connect/client";

/**
 * useDistinctTags fetches the whole-ObjectKey distinct tag key→values that back
 * the tag-facet filter, via ObjectTagService.ListDistinctTags. It returns a
 * sorted `"key=value"` option list — the shape ObjectsFilterBar expects.
 *
 * The list is authoritative across the tenant's objects (not just the loaded
 * page). On error, or while the tenant / objectKey is unresolved, it returns an
 * empty list and the page falls back to client-side accumulation from the
 * objects already loaded — so the dropdown degrades gracefully.
 */
export function useDistinctTags(objectKey: string): string[] {
  const { user } = useAuth();
  const tenantId = user?.tenantId ?? "";
  const [options, setOptions] = useState<string[]>([]);

  useEffect(() => {
    if (!tenantId || !objectKey) {
      setOptions([]);
      return;
    }
    let cancelled = false;
    const parent = `tenants/${tenantId}/objectKeys/${objectKey}`;
    objectTagClient
      .listDistinctTags({ parent })
      .then((res) => {
        if (cancelled) return;
        const pairs: string[] = [];
        for (const [key, tv] of Object.entries(res.tags)) {
          for (const value of tv.values) pairs.push(`${key}=${value}`);
        }
        pairs.sort();
        setOptions(pairs);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        // Non-fatal: the dropdown still works off client-side accumulation.
        console.debug("listDistinctTags failed", ConnectError.from(e).message);
        setOptions([]);
      });
    return () => {
      cancelled = true;
    };
  }, [tenantId, objectKey]);

  return options;
}
