"use client";

import { useCallback } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

import { auditClient } from "@/lib/connect/client";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";
import { normalizeError } from "@/lib/connect/error";
import { API_PAGE_SIZE_MAX } from "@/constants";
import type { SortState } from "@/hooks/useTableSort";
import type { AuditOrderField } from "@/lib/auditOrder";
import { toSortOrder } from "@/lib/table/sortOrder";

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

/** No sort: the log's own order, newest first. */
const NO_SORT: SortState<AuditOrderField> = { column: null, direction: null };

/**
 * `tenantId` narrows the log to that tenant's trail — what its principals did
 * and what was done to it — in the server's query, so every page is full of
 * the tenant's entries. Empty = every tenant.
 *
 * `sort` is applied by the server, which pages a sorted log; the sort is part
 * of the query key, so a new one starts again from the first page rather than
 * resuming a cursor minted for another order.
 */
export function useAuditLogs(
  pageSize: number = 50,
  filter: string = "",
  tenantId: string = "",
  sort: SortState<AuditOrderField> = NO_SORT,
): UseAuditLogsResult {
  const orderBy = sort.direction ? (sort.column ?? "") : "";
  const sortOrder = toSortOrder(orderBy ? sort.direction : null);
  const query = useInfiniteQuery({
    queryKey: ["auditLogs", pageSize, filter, tenantId, orderBy, sortOrder],
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
            tenantId,
            orderBy,
            sortOrder,
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

  // refetch is stable; query is a new object on every render.
  const { refetch } = query;
  const refresh = useCallback(async () => {
    await refetch();
  }, [refetch]);
  const { hasNextPage, fetchNextPage } = query;
  const loadMore = useCallback(async () => {
    if (hasNextPage) await fetchNextPage();
  }, [hasNextPage, fetchNextPage]);

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
