"use client";

import { keepPreviousData, useQuery } from "@tanstack/react-query";

import { adminSystemClient } from "@/lib/connect/client";
import { normalizeError } from "@/lib/connect/error";
import type { HealthAll } from "@/lib/health";
import { healthAllFromResponse } from "@/lib/platformHealth";

// usePlatformHealth — admin/v1.SystemService.GetPlatformHealth: every role's
// health, read by the admin role (ADR-0048). The caller decides when to
// refresh; a failed read keeps the last answer on screen.
export function usePlatformHealth() {
  const query = useQuery({
    queryKey: ["admin", "platform-health"],
    queryFn: async (): Promise<HealthAll> => {
      try {
        return healthAllFromResponse(
          await adminSystemClient.getPlatformHealth({}),
        );
      } catch (err) {
        throw normalizeError(err);
      }
    },
    placeholderData: keepPreviousData,
  });
  return {
    data: query.data ?? null,
    loading: query.isFetching,
    error: (query.error as Error | null) ?? null,
    /** When the answer on screen arrived; null before the first. */
    syncedAt: query.dataUpdatedAt ? new Date(query.dataUpdatedAt) : null,
    refresh: async () => {
      await query.refetch();
    },
  };
}
