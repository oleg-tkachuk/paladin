"use client";

import { useQuery } from "@tanstack/react-query";

import { purgePreviewQuery } from "@/lib/queries/tenants";

/**
 * The purge preview of the tenant, read while `tenantId` is set — while the
 * purge confirmation is open for it.
 */
export function usePurgePreview(tenantId: string | undefined) {
  return useQuery({
    ...purgePreviewQuery(tenantId ?? ""),
    enabled: !!tenantId,
  });
}
