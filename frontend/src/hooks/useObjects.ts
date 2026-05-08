"use client";

import { useCallback, useEffect, useMemo, useState } from "react";

import { objectClient, batchClient, presignClient } from "@/lib/connect/client";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import type { PresignedUrl } from "@/gen/paladin/common/v1/resource_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import { useAuth } from "@/context/AuthContext";
import { useRefreshSignal, useBumpRefresh } from "@/context/RefreshContext";
import { API_LIMIT_DEFAULT } from "@/constants";

/**
 * useObjects — wrapper around data/v1.ObjectService + BatchService.
 *
 * Resource layout:
 *   parent ObjectKey  → "tenants/{tenant}/objectKeys/{ok}"
 *   Object name       → opaque (returned by ListObjects, used for delete /
 *                        copy / presign)
 *
 * Bulk operations group by destination ObjectKey because BatchService
 * requires a single `parent` per call. The page already passes
 * `{name, objectKey}[]` so the wrapper buckets by `objectKey` and issues
 * one Batch RPC per group.
 */

export interface UseObjectsOptions {
  /** ObjectKey id (slug or uuid) — assembled with the user's tenant. */
  objectKey?: string;
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

type BulkItem = { name: string; objectKey: string };

function groupByObjectKey(items: BulkItem[]): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const it of items) {
    const arr = out.get(it.objectKey) ?? [];
    arr.push(it.name);
    out.set(it.objectKey, arr);
  }
  return out;
}

export function useObjects(options: UseObjectsOptions = {}) {
  const { user } = useAuth();
  const tenantId = user?.tenantId ?? "";
  const objectKey = options.objectKey ?? DEFAULT_OBJECT_KEY;
  const parent = useMemo(
    () => (tenantId ? `tenants/${tenantId}/objectKeys/${objectKey}` : ""),
    [tenantId, objectKey],
  );
  const parentFor = useCallback(
    (ok: string) => (tenantId ? `tenants/${tenantId}/objectKeys/${ok}` : ""),
    [tenantId],
  );

  const [objects, setObjects] = useState<Object$[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);

  const refreshSignal = useRefreshSignal("objects");
  const bumpRefresh = useBumpRefresh();

  const fetchPage = useCallback(
    async (pageToken: string = "") => {
      if (!parent) return { objects: [] as Object$[], nextCursor: undefined };
      setLoading(true);
      setError(null);
      try {
        const res = await objectClient.listObjects({
          parent,
          page: {
            pageSize: options.pageSize ?? options.limit ?? API_LIMIT_DEFAULT,
            pageToken,
          },
          filter: options.filter ?? "",
          orderBy: options.orderBy ?? "",
          sortOrder: toSortOrder(options.sortDirection),
        });
        const next = res.page?.nextPageToken || undefined;
        if (pageToken) {
          setObjects((prev) => [...prev, ...res.objects]);
        } else {
          setObjects(res.objects);
        }
        setNextCursor(next);
        return { objects: res.objects, nextCursor: next };
      } catch (err) {
        setError(err as Error);
        throw err;
      } finally {
        setLoading(false);
      }
    },
    [
      parent,
      options.pageSize,
      options.limit,
      options.filter,
      options.orderBy,
      options.sortDirection,
    ],
  );

  // Initial load + refetch on options change. Also re-runs whenever any
  // page bumps the "objects" refresh signal (uploads, deletes elsewhere, …).
  useEffect(() => {
    void fetchPage("");
  }, [fetchPage, refreshSignal]);

  const refresh = useCallback(async () => fetchPage(""), [fetchPage]);
  const loadMore = nextCursor ? () => fetchPage(nextCursor) : undefined;

  const softDeleteObject = useCallback(
    async (obj: Object$): Promise<void> => {
      await objectClient.deleteObject({
        name: obj.name,
        resourceVersion: obj.resourceVersion,
        permanent: false,
        bypassGovernanceRetention: false,
      });
      setObjects((prev) => prev.filter((o) => o.objectId !== obj.objectId));
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
      setObjects((prev) => prev.filter((o) => o.objectId !== obj.objectId));
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
      // Caller usually refreshes; nothing to mutate locally.
      bumpRefresh("objects");
    },
    [bumpRefresh],
  );

  const copyObject = useCallback(
    async (
      sourceName: string,
      destinationKey: string,
      destinationObjectKey: string,
    ): Promise<void> => {
      await objectClient.copyObject({
        sourceName,
        destinationObjectKey: parentFor(destinationObjectKey),
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
      const groups = groupByObjectKey(items);
      await Promise.all(
        Array.from(groups.entries()).map(([ok, names]) =>
          batchClient.batchDeleteObjects({
            parent: parentFor(ok),
            selector: { names, filter: "" },
            permanent: opts.permanent ?? false,
          }),
        ),
      );
      const namesSet = new Set(items.map((i) => i.name));
      setObjects((prev) => prev.filter((o) => !namesSet.has(o.name)));
      bumpRefresh("objects");
    },
    [parentFor, bumpRefresh],
  );

  const bulkRestoreObjects = useCallback(
    async (items: BulkItem[]) => {
      const groups = groupByObjectKey(items);
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
      // Group affected objects by their parent + intended tag set. The page
      // currently calls this with one tag-map for all items, so we batch
      // them per ObjectKey under the same selector.
      const tags = items[0]?.tags ?? {};
      const namesByOk = new Map<string, string[]>();
      for (const it of items) {
        const obj = objects.find((o) => o.objectId === it.objectId);
        if (!obj) continue;
        const arr = namesByOk.get(obj.objectKey) ?? [];
        arr.push(obj.name);
        namesByOk.set(obj.objectKey, arr);
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
    loading,
    error,
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
