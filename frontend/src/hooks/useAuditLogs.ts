"use client";

import { useCallback } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

import { auditClient } from "@/lib/connect/client";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";
import { normalizeError } from "@/lib/connect/error";
import { API_PAGE_SIZE_MAX } from "@/constants";

// useAuditLogs — wrapper around admin/v1.AuditLogService.ListAuditLog.
//
// Backed by TanStack useInfiniteQuery: the server-issued page_token drives
// pagination, loadMore appends a page, refresh resets to page 1. Filter is a
// CEL expression accepted as-is by the backend (see proto comment on
// ListAuditLogRequest.filter).

export type { AuditLogEntry };

export interface UseAuditLogsResult {
  entries: AuditLogEntry[];
  loading: boolean;
  error: string | null;
  nextCursor: string;
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
}

export function useAuditLogs(
  pageSize: number = 50,
  filter: string = "",
): UseAuditLogsResult {
  const query = useInfiniteQuery({
    queryKey: ["auditLogs", pageSize, filter],
    initialPageParam: "",
    queryFn: async ({ pageParam, signal }) => {
      try {
        return await auditClient.listAuditLog(
          {
            page: {
              pageSize: Math.min(pageSize, API_PAGE_SIZE_MAX),
              pageToken: pageParam,
            },
            filter,
          },
          { signal },
        );
      } catch (err) {
        throw normalizeError(err);
      }
    },
    getNextPageParam: (last) => last.page?.nextPageToken || undefined,
  });

  const entries = query.data?.pages.flatMap((p) => p.entries) ?? [];

  const refresh = useCallback(async () => {
    await query.refetch();
  }, [query]);
  const loadMore = useCallback(async () => {
    if (query.hasNextPage) await query.fetchNextPage();
  }, [query]);

  return {
    entries,
    loading: query.isFetching,
    // The old hook surfaced the server's rawMessage string; normalizeError
    // already unwraps a ConnectError to that.
    error: query.error ? (query.error as Error).message : null,
    // Truthy when more pages exist — callers gate the "load more" control on it.
    nextCursor: query.hasNextPage
      ? (query.data?.pages.at(-1)?.page?.nextPageToken ?? "")
      : "",
    refresh,
    loadMore,
  };
}
