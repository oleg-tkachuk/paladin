"use client";

import { useCallback } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

import { adminOperationClient } from "@/lib/connect/client";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import { normalizeError } from "@/lib/connect/error";
import type { FacetCount } from "@/gen/paladin/common/v1/facet_pb";
import {
  operationFacetsRequest,
  operationsFilter,
  type OpView,
} from "@/lib/operations";
import type { FacetSelection } from "@/lib/table/facets";

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

/** The view that keeps only work that left something undone. */
const FAILURES_VIEW: OpView = "failed";
/** The view that keeps everything. */
const ALL_VIEW: OpView = "all";

export interface UseOperationsArgs {
  /** The state and type facets' choices, which the server narrows by. */
  facets: FacetSelection;
  /**
   * Only work that left something undone: failed, or a batch that left
   * objects behind — which the state facet cannot say, a partly failed
   * batch being succeeded there.
   */
  failuresOnly: boolean;
  scope: OpScope;
  /** With scope "mine", the operations the user cleared too. */
  includeDismissed: boolean;
  live: boolean;
}

export interface UseOperationsResult {
  operations: Operation[];
  /** The facets counted over the whole listing, with its first page. */
  facetCounts: readonly FacetCount[];
  loading: boolean;
  error: string | null;
  hasMore: boolean;
  refresh: () => Promise<void>;
  loadMore: () => Promise<void>;
}

export function useOperations({
  facets,
  failuresOnly,
  scope,
  includeDismissed,
  live,
}: UseOperationsArgs): UseOperationsResult {
  const filter = operationsFilter(failuresOnly ? FAILURES_VIEW : ALL_VIEW);
  const narrow = operationFacetsRequest(facets);
  const mine = scope === "mine";
  // Cleared is a mark on the user's own list; the tenant's list shows them all.
  const withDismissed = mine && includeDismissed;
  const query = useInfiniteQuery({
    queryKey: ["operations", filter, narrow, mine, withDismissed],
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
            facets: narrow,
            // Counted once, with the first page: a later one is the same
            // listing, and the server answers it without counts.
            countFacets: pageParam === "",
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
    facetCounts: query.data?.pages[0]?.facetCounts ?? NO_COUNTS,
    loading: query.isFetching,
    error: query.error ? (query.error as Error).message : null,
    hasMore: hasNextPage,
    refresh,
    loadMore,
  };
}

const NO_COUNTS: readonly FacetCount[] = [];
