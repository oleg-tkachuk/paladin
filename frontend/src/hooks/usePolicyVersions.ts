"use client";

import { useCallback, useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

import type { PolicyVersion } from "@/gen/paladin/admin/v1/policy_service_pb";
import { errorMessage } from "@/hooks/errorContract";
import { policyVersionsQuery } from "@/lib/queries/policyVersions";

export interface UsePolicyVersionsResult {
  /** Every version read so far, newest first. */
  versions: PolicyVersion[];
  /** The first page has not answered yet. */
  loading: boolean;
  /** An older page is being read. */
  loadingMore: boolean;
  /** The history could not be read: the list is not the layer's history. */
  error: string | null;
  /** The layer does not exist, or the caller cannot see it. */
  notFound: boolean;
  hasMore: boolean;
  loadMore: () => Promise<void>;
  refresh: () => Promise<void>;
}

/**
 * The texts one policy layer has held, newest first, read a page at a time:
 * `loadMore` reads the next older page. "" names no layer and reads nothing.
 */
export function usePolicyVersions(
  resourceName: string,
): UsePolicyVersionsResult {
  const history = useInfiniteQuery(policyVersionsQuery(resourceName));
  const pages = history.data?.pages;
  const versions = useMemo(
    () => (pages ?? []).flatMap((p) => p.versions),
    [pages],
  );
  const { fetchNextPage, refetch } = history;
  const loadMore = useCallback(async () => {
    await fetchNextPage();
  }, [fetchNextPage]);
  const refresh = useCallback(async () => {
    await refetch();
  }, [refetch]);

  return {
    versions,
    loading: history.isPending && resourceName !== "",
    loadingMore: history.isFetchingNextPage,
    error: history.error
      ? errorMessage(history.error, "Failed to load the policy history")
      : null,
    notFound: pages?.[0]?.notFound ?? false,
    hasMore: history.hasNextPage,
    loadMore,
    refresh,
  };
}
