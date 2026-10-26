"use client";

import { useCallback, useMemo } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";

import { objectClient, batchClient, presignClient } from "@/lib/connect/client";
import { selectorBatches, type BatchItem } from "@/lib/batchSelector";
import type { Object$, ObjectState } from "@/gen/paladin/data/v1/types_pb";
import type { ObjectContentCategory } from "@/gen/paladin/data/v1/object_service_pb";
import type { FacetCount } from "@/gen/paladin/common/v1/facet_pb";
import type { PresignedUrl } from "@/gen/paladin/common/v1/resource_pb";
import { toSortOrder } from "@/lib/table/sortOrder";
import { useActingTenantId } from "@/hooks/useActingTenant";
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
  /** Asks again this often while the tab is visible; unset, it does not. */
  refetchIntervalMs?: number;
  /** Keys under this prefix only. */
  prefix?: string;
  /** Set, one level under `prefix`, with the folders below it. */
  delimiter?: string;
  /** The states and content categories the server narrows by, beside `filter`. */
  facets?: {
    states: readonly ObjectState[];
    contentCategories: readonly ObjectContentCategory[];
  };
  /** Ask the server to count the facets with the first page. */
  countFacets?: boolean;
}

const NO_FACET_COUNTS: readonly FacetCount[] = [];

const DEFAULT_OBJECT_KEY = "default";

type BulkItem = BatchItem;

export function useObjects(options: UseObjectsOptions = {}) {
  const tenantId = useActingTenantId();
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
      options.prefix ?? "",
      options.delimiter ?? "",
      options.facets ?? null,
      options.countFacets ?? false,
      pageSize,
      refreshSignal,
    ],
    enabled: !!parent,
    refetchInterval: options.refetchIntervalMs ?? false,
    initialPageParam: "",
    queryFn: async ({ pageParam }) => {
      try {
        return await objectClient.listObjects({
          parent,
          page: { pageSize, pageToken: pageParam },
          filter: options.filter ?? "",
          orderBy: options.orderBy ?? "",
          sortOrder: toSortOrder(options.sortDirection),
          prefix: options.prefix ?? "",
          delimiter: options.delimiter ?? "",
          facets: options.facets && {
            states: [...options.facets.states],
            contentCategories: [...options.facets.contentCategories],
          },
          // Counted once, with the first page: a later one is the same
          // listing, and the server answers it without counts.
          countFacets: !!options.countFacets && pageParam === "",
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
  // The server answers the folders with the first page only.
  const firstPage = query.data?.pages[0];
  const folders = useMemo(() => firstPage?.commonPrefixes ?? [], [firstPage]);
  const foldersTruncated = firstPage?.commonPrefixesTruncated ?? false;
  const facetCounts = firstPage?.facetCounts ?? NO_FACET_COUNTS;
  const facetCountsExact = firstPage?.facetCountsExact ?? false;
  const nextCursor = query.hasNextPage
    ? (query.data?.pages.at(-1)?.page?.nextPageToken ?? undefined)
    : undefined;

  // query is a new object on every render; its refetch is stable. Depending
  // on query gave refresh a new identity per render, and the objects page
  // re-registers its palette actions whenever refresh changes — which updates
  // the actions context and renders the page again: an endless loop.
  const { refetch } = query;
  const refresh = useCallback(async () => {
    await refetch();
  }, [refetch]);
  const loadMore = query.hasNextPage
    ? () => {
        void query.fetchNextPage();
      }
    : undefined;

  // Mutations: fire the RPC, then bumpRefresh("objects") → queryKey changes →
  // the list refetches. (Replaces the old optimistic local setObjects edits.)
  const softDeleteObject = useCallback(
    /** Resolves to the object as the trash holds it, for an Undo. */
    async (obj: Object$): Promise<Object$ | undefined> => {
      const res = await objectClient.deleteObject({
        name: obj.name,
        resourceVersion: obj.resourceVersion,
        permanent: false,
        bypassGovernanceRetention: false,
      });
      bumpRefresh("objects");
      return res.object;
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
      await Promise.all(
        selectorBatches(items).map(({ collection: ok, names }) =>
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
      await Promise.all(
        selectorBatches(items).map(({ collection: ok, names }) =>
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
      const named = items.flatMap((it) => {
        const obj = objects.find((o) => o.objectId === it.objectId);
        return obj ? [{ name: obj.name, collection: obj.collection }] : [];
      });
      await Promise.all(
        selectorBatches(named).map(({ collection: ok, names }) =>
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

  /**
   * Copies the items into another collection of this tenant, each under its
   * own key behind `keyPrefix`. Runs as one operation per batch; the objects
   * appear in the destination as the operations finish.
   */
  const bulkCopyObjects = useCallback(
    async (
      items: BulkItem[],
      destinationCollection: string,
      keyPrefix: string,
    ) => {
      await Promise.all(
        selectorBatches(items).map(({ collection: ok, names }) =>
          batchClient.batchCopyObjects({
            sourceParent: parentFor(ok),
            selector: { names, filter: "" },
            destinationCollection: parentFor(destinationCollection),
            destinationKeyPrefix: keyPrefix,
          }),
        ),
      );
      bumpRefresh("objects");
    },
    [parentFor, bumpRefresh],
  );

  return {
    objects,
    folders,
    foldersTruncated,
    facetCounts,
    facetCountsExact,
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
    bulkCopyObjects,
  };
}
