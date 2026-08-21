"use client";

import { useCallback, useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

import { objectClient, batchClient, presignClient } from "@/lib/connect/client";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import type { PresignedUrl } from "@/gen/paladin/common/v1/resource_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import { useAuth } from "@/context/AuthContext";
import { useRefreshSignal, useBumpRefresh } from "@/context/RefreshContext";
import { normalizeError } from "@/lib/connect/error";
import { API_LIMIT_DEFAULT } from "@/constants";

/**
 * useObjects — wrapper around data/v1.ObjectService + BatchService.
 *
 * Resource layout:
 *   parent Collection  → "tenants/{tenant}/collections/{ok}"
 *   Object name       → opaque (returned by ListObjects, used for delete /
 *                        copy / presign)
 *
 * Backed by TanStack `useInfiniteQuery`: the list + cursor pagination live in
 * the query cache. The cross-component "objects" refresh signal is folded
 * into the queryKey, so any `bumpRefresh("objects")` (uploads / deletes here
 * or elsewhere) changes the key and TanStack refetches from page 1 — the same
 * reset-to-page-1 behaviour the old setState-in-effect had, without the
 * effect.
 *
 * Bulk operations group by destination Collection because BatchService
 * requires a single `parent` per call.
 */

export interface UseObjectsOptions {
  /** Collection id (slug or uuid) — assembled with the user's tenant. */
  collection?: string;
  /** CEL filter passed straight through to ListObjects.filter. */
  filter?: string;
  /** Field name to order by (e.g. "created_at"). */
  orderBy?: string;
  /** Page size override; defaults to API_LIMIT_DEFAULT. */
  pageSize?: number;
  /** Legacy alias for `pageSize` — kept for /trash callers. */
  limit?: number;
  /** Initial page-token for pagination. */
  initialCursor?: string;
  /** Sort direction for the active orderBy column. */
  sortDirection?: "asc" | "desc" | null;
}

const DEFAULT_OBJECT_KEY = "default";

function toSortOrder(d: UseObjectsOptions["sortDirection"]): SortOrder {
  if (d === "asc") return SortOrder.ASC;
  if (d === "desc") return SortOrder.DESC;
  return SortOrder.UNSPECIFIED;
}

type BulkItem = { name: string; collection: string };

function groupByCollection(items: BulkItem[]): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const it of items) {
    const arr = out.get(it.collection) ?? [];
    arr.push(it.name);
    out.set(it.collection, arr);
  }
  return out;
}

export function useObjects(options: UseObjectsOptions = {}) {
  const { user } = useAuth();
  const tenantId = user?.tenantId ?? "";
  const collection = options.collection ?? DEFAULT_OBJECT_KEY;
  const parent = useMemo(
    () => (tenantId ? `tenants/${tenantId}/collections/${collection}` : ""),
    [tenantId, collection],
  );
  const parentFor = useCallback(
    (ok: string) => (tenantId ? `tenants/${tenantId}/collections/${ok}` : ""),
    [tenantId],
  );

  const refreshSignal = useRefreshSignal("objects");
  const bumpRefresh = useBumpRefresh();
  const pageSize = options.pageSize ?? options.limit ?? API_LIMIT_DEFAULT;

  const query = useInfiniteQuery({
    // refreshSignal in the key: a bump → new key → refetch from page 1.
    queryKey: [
      "objects",
      parent,
      options.filter ?? "",
      options.orderBy ?? "",
      options.sortDirection ?? null,
      pageSize,
      refreshSignal,
    ],
    enabled: !!parent,
    initialPageParam: "",
    queryFn: async ({ pageParam }) => {
      try {
        return await objectClient.listObjects({
          parent,
          page: { pageSize, pageToken: pageParam },
          filter: options.filter ?? "",
          orderBy: options.orderBy ?? "",
          sortOrder: toSortOrder(options.sortDirection),
        });
      } catch (err) {
        throw normalizeError(err);
      }
    },
    getNextPageParam: (last) => last.page?.nextPageToken || undefined,
  });

  const objects: Object$[] = useMemo(
    () => query.data?.pages.flatMap((p) => p.objects) ?? [],
    [query.data],
  );
  const nextCursor = query.hasNextPage
    ? (query.data?.pages.at(-1)?.page?.nextPageToken ?? undefined)
    : undefined;

  const refresh = useCallback(async () => {
    await query.refetch();
  }, [query]);
  const loadMore = query.hasNextPage
    ? () => {
        void query.fetchNextPage();
      }
    : undefined;

  // Mutations: fire the RPC, then bumpRefresh("objects") → queryKey changes →
  // the list refetches. (Replaces the old optimistic local setObjects edits.)
  const softDeleteObject = useCallback(
    async (obj: Object$): Promise<void> => {
      await objectClient.deleteObject({
        name: obj.name,
        resourceVersion: obj.resourceVersion,
        permanent: false,
        bypassGovernanceRetention: false,
      });
      bumpRefresh("objects");
    },
    [bumpRefresh],
  );

  const purgeObject = useCallback(
    async (obj: Object$): Promise<void> => {
      await objectClient.deleteObject({
        name: obj.name,
        resourceVersion: obj.resourceVersion,
        permanent: true,
        bypassGovernanceRetention: false,
      });
      bumpRefresh("objects");
    },
    [bumpRefresh],
  );

  const restoreObject = useCallback(
    async (obj: Object$): Promise<void> => {
      await objectClient.restoreObject({
        name: obj.name,
        resourceVersion: obj.resourceVersion,
      });
      bumpRefresh("objects");
    },
    [bumpRefresh],
  );

  const copyObject = useCallback(
    async (
      sourceName: string,
      destinationKey: string,
      destinationCollection: string,
    ): Promise<void> => {
      await objectClient.copyObject({
        sourceName,
        destinationCollection: parentFor(destinationCollection),
        destinationKey,
      });
      bumpRefresh("objects");
    },
    [parentFor, bumpRefresh],
  );

  const generateDownloadUrl = useCallback(
    async (name: string): Promise<PresignedUrl | null> => {
      const res = await presignClient.presignDownload({
        name,
        contentDisposition: "",
      });
      return res.downloadUrl ?? null;
    },
    [],
  );

  const regenerateUploadUrl = useCallback(
    async (name: string): Promise<PresignedUrl | null> => {
      const res = await presignClient.regenerateUploadUrl({ name });
      return res.uploadUrl ?? null;
    },
    [],
  );

  const bulkDeleteObjects = useCallback(
    async (items: BulkItem[], opts: { permanent?: boolean } = {}) => {
      const groups = groupByCollection(items);
      await Promise.all(
        Array.from(groups.entries()).map(([ok, names]) =>
          batchClient.batchDeleteObjects({
            parent: parentFor(ok),
            selector: { names, filter: "" },
            permanent: opts.permanent ?? false,
          }),
        ),
      );
      bumpRefresh("objects");
    },
    [parentFor, bumpRefresh],
  );

  const bulkRestoreObjects = useCallback(
    async (items: BulkItem[]) => {
      const groups = groupByCollection(items);
      await Promise.all(
        Array.from(groups.entries()).map(([ok, names]) =>
          batchClient.batchRestoreObjects({
            parent: parentFor(ok),
            selector: { names, filter: "" },
          }),
        ),
      );
      bumpRefresh("objects");
    },
    [parentFor, bumpRefresh],
  );

  const bulkPatchObjects = useCallback(
    async (items: { objectId: string; tags: Record<string, string> }[]) => {
      // The page calls this with one tag-map for all items, so we batch them
      // per Collection under the same selector.
      const tags = items[0]?.tags ?? {};
      const namesByOk = new Map<string, string[]>();
      for (const it of items) {
        const obj = objects.find((o) => o.objectId === it.objectId);
        if (!obj) continue;
        const arr = namesByOk.get(obj.collection) ?? [];
        arr.push(obj.name);
        namesByOk.set(obj.collection, arr);
      }
      await Promise.all(
        Array.from(namesByOk.entries()).map(([ok, names]) =>
          batchClient.batchUpdateTags({
            parent: parentFor(ok),
            selector: { names, filter: "" },
            tags,
            replace: false,
          }),
        ),
      );
      bumpRefresh("objects");
    },
    [objects, parentFor, bumpRefresh],
  );

  return {
    objects,
    loading: query.isFetching,
    error: (query.error as Error | null) ?? null,
    nextCursor,
    refresh,
    loadMore,
    softDeleteObject,
    restoreObject,
    purgeObject,
    copyObject,
    generateDownloadUrl,
    regenerateUploadUrl,
    bulkDeleteObjects,
    bulkRestoreObjects,
    bulkPatchObjects,
  };
}
