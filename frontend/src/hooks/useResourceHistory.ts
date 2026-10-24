"use client";

import { useCallback, useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

import { useAuditLogs } from "@/hooks/useAuditLogs";
import { adminOperationClient } from "@/lib/connect/client";
import { normalizeError } from "@/lib/connect/error";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import {
  operationsFilterFor,
  historyFromAudit,
  historyFromOperation,
  mergeHistory,
  type HistoryEntry,
} from "@/lib/history";
import { isActive, operationStatus } from "@/lib/operations";

/** Entries a page of each record holds. */
const HISTORY_PAGE_SIZE = 50;
/** How often the history asks again while an operation in it runs. */
const RUNNING_POLL_MS = 5_000;

/** Whose history: a tenant's trail, or one resource's own entries. */
export type HistoryScope =
  { kind: "tenant"; tenantId: string } | { kind: "resource"; filter: string };

export interface UseResourceHistoryResult {
  entries: HistoryEntry[];
  loading: boolean;
  /** The audit log could not be read: the timeline is not the history. */
  error: string | null;
  /** The operations could not be read: the timeline lacks the batches. */
  operationsError: string | null;
  hasMore: boolean;
  loadMore: () => Promise<void>;
  refresh: () => Promise<void>;
}

/**
 * A resource's history as one timeline. A tenant's reads its audit trail, as
 * the server selects it, and its operations — the batches its collections
 * ran, which the audit log does not hold. Another resource's reads the audit
 * entries that name it: operations act on collections, not on a bucket or a
 * backend.
 *
 * Each record pages on its own, newest first, so the merged list is exact
 * down to the older of the two last-loaded entries; "load more" reads the
 * next page of both.
 */
export function useResourceHistory(
  scope: HistoryScope,
): UseResourceHistoryResult {
  const tenantId = scope.kind === "tenant" ? scope.tenantId : "";
  const filter = scope.kind === "resource" ? scope.filter : "";
  const audit = useAuditLogs(HISTORY_PAGE_SIZE, filter, tenantId);

  // The tenant's operations, named: the server lists the caller's own
  // tenant's unless told which, so a platform admin on another tenant's page
  // saw none of its batches. The filter holds a server from before tenant_id
  // to this tenant's too: during a rollout it showed the caller's own.
  const ops = useInfiniteQuery({
    queryKey: ["operations", "history", tenantId],
    enabled: tenantId !== "",
    initialPageParam: "",
    queryFn: async ({ pageParam, signal }) => {
      try {
        return await adminOperationClient.listOperations(
          {
            page: { pageSize: HISTORY_PAGE_SIZE, pageToken: pageParam },
            tenantId,
            filter: operationsFilterFor(tenantId),
            sortOrder: SortOrder.DESC,
          },
          { signal },
        );
      } catch (err) {
        throw normalizeError(err);
      }
    },
    getNextPageParam: (last) => last.page?.nextPageToken || undefined,
    // A running operation moves on the timeline without a click.
    refetchInterval: (q) =>
      q.state.data?.pages.some((p) =>
        p.operations.some((o) => isActive(operationStatus(o))),
      )
        ? RUNNING_POLL_MS
        : false,
  });

  const opPages = ops.data?.pages;
  const entries = useMemo(() => {
    const now = new Date();
    return mergeHistory(
      audit.entries.map(historyFromAudit),
      (opPages ?? []).flatMap((p) =>
        p.operations.map((o) => historyFromOperation(o, now)),
      ),
    );
  }, [audit.entries, opPages]);

  const { loadMore: loadMoreAudit, refresh: refreshAudit, nextCursor } = audit;
  const { hasNextPage, fetchNextPage, refetch } = ops;
  const loadMore = useCallback(async () => {
    await Promise.all([
      nextCursor ? loadMoreAudit() : undefined,
      hasNextPage ? fetchNextPage() : undefined,
    ]);
  }, [nextCursor, loadMoreAudit, hasNextPage, fetchNextPage]);
  const refresh = useCallback(async () => {
    await Promise.all([refreshAudit(), tenantId ? refetch() : undefined]);
  }, [refreshAudit, refetch, tenantId]);

  return {
    entries,
    loading: audit.loading || ops.isFetching,
    error: audit.error,
    operationsError: ops.error ? (ops.error as Error).message : null,
    hasMore: nextCursor !== "" || hasNextPage,
    loadMore,
    refresh,
  };
}
