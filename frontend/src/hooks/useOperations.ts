"use client";

import { useCallback } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

import { adminOperationClient } from "@/lib/connect/client";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import { normalizeError } from "@/lib/connect/error";
import { operationsFilter, type OpView } from "@/lib/operations";

// useOperations — admin/v1.OperationService.ListOperations, a page at a time,
// newest first, for the operations page. The drawer reads the shell's copy
// instead; this one is the page's own, with its own filters and paging.
//
// Live, every loaded page is fetched again on an interval while the tab is
// visible (TanStack pauses refetchInterval in a hidden tab), so a running
// batch moves on the page without a click.

/** Operations one page of the list holds. */
const OPERATIONS_PAGE_SIZE = 50;
/** How often a live list asks again. */
export const OPERATIONS_POLL_MS = 2_000;

/** Whose operations: the signed-in user's, or everyone's in their tenant. */
export type OpScope = "mine" | "tenant";

export interface UseOperationsArgs {
  view: OpView;
  scope: OpScope;
  /** With scope "mine", the operations the user cleared too. */
  includeDismissed: boolean;
  live: boolean;
}

export interface UseOperationsResult {
  operations: Operation[];
  loading: boolean;
  error: string | null;
  hasMore: boolean;
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
}

export function useOperations({
  view,
  scope,
  includeDismissed,
  live,
}: UseOperationsArgs): UseOperationsResult {
  const filter = operationsFilter(view);
  const mine = scope === "mine";
  // Cleared is a mark on the user's own list; the tenant's list shows them all.
  const withDismissed = mine && includeDismissed;
  const query = useInfiniteQuery({
    queryKey: ["operations", filter, mine, withDismissed],
    initialPageParam: "",
    queryFn: async ({ pageParam, signal }) => {
      try {
        return await adminOperationClient.listOperations(
          {
            page: { pageSize: OPERATIONS_PAGE_SIZE, pageToken: pageParam },
            filter,
            sortOrder: SortOrder.DESC,
            mine,
            includeDismissed: withDismissed,
          },
          { signal },
        );
      } catch (err) {
        throw normalizeError(err);
      }
    },
    getNextPageParam: (last) => last.page?.nextPageToken || undefined,
    refetchInterval: live ? OPERATIONS_POLL_MS : false,
  });

  const { refetch, hasNextPage, fetchNextPage } = query;
  const refresh = useCallback(async () => {
    await refetch();
  }, [refetch]);
  const loadMore = useCallback(async () => {
    if (hasNextPage) await fetchNextPage();
  }, [hasNextPage, fetchNextPage]);

  return {
    operations: query.data?.pages.flatMap((p) => p.operations) ?? [],
    loading: query.isFetching,
    error: query.error ? (query.error as Error).message : null,
    hasMore: hasNextPage,
    refresh,
    loadMore,
  };
}
