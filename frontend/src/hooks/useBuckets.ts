"use client";

import { useCallback, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { ConnectError } from "@connectrpc/connect";

import { bucketClient } from "@/lib/connect/client";
import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { BucketSchema } from "@/gen/paladin/admin/v1/types_pb";
import { useBumpRefresh } from "@/context/RefreshContext";
import { API_PAGE_SIZE_MAX } from "@/constants";

// Ceiling on how many pages one fetchBuckets call will follow. At the maximum
// page size this is 10k buckets — far past any console list that stays usable,
// and a backstop against paging forever if a token ever fails to terminate.
const MAX_LIST_PAGES = 20;

/**
 * useBuckets — wrapper around admin/v1.BucketService.
 *
 * Resource name layout: `storageBackends/{backend_id}/buckets/{bucket_name}`.
 * Create takes `parent + bucket_name + bucket(resource)`; Update takes
 * `name + resource_version + update_mask + bucket(resource)`; Delete uses
 * `delete_on_backend` (renamed from the old `delete_remote`).
 *
 * Page-level callers pass legacy positional args (backendId, bucketId, …)
 * — the wrapper translates them into the nested request shapes so pages
 * don't need to know about the new schema.
 */

const backendParent = (backendId: string) => `storageBackends/${backendId}`;
const bucketResourceName = (backendId: string, bucketId: string) =>
  `storageBackends/${backendId}/buckets/${bucketId}`;

export function useBuckets() {
  const bumpRefresh = useBumpRefresh();
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchBuckets = useCallback(
    async (
      backendId?: string,
      filter: string = "",
      pageToken: string = "",
      ownerTenantId?: string,
    ) => {
      setLoading(true);
      setError(null);
      try {
        // Follows nextPageToken to the end rather than returning one page.
        //
        // Every caller — the scope picker, the bucket selectors in the
        // collection and tenant dialogs, the /buckets tables — asks for "the
        // buckets" and got the first API_PAGE_SIZE_MAX of them, ordered by
        // backend then name. Past that ceiling a bucket was invisible and
        // unselectable everywhere in the console, with nothing on screen
        // saying so; the deployment that crossed 500 buckets started losing
        // whichever names sorted last. The server-side `filter` is no escape
        // hatch: ListBuckets evaluates CEL over the page it already read, so
        // searching cannot reach rows the page never contained.
        //
        // MAX_PAGES bounds a pathological account rather than the normal one;
        // the residual token is still returned so a caller can continue.
        let token = pageToken;
        let pages = 0;
        const acc: Bucket[] = [];
        do {
          // ownerTenantId pushes the tenant-narrow filter to the
          // server (uses the partial index on buckets.owner_tenant_id).
          // Empty/undefined preserves the cross-tenant listing for
          // platform-admin views.
          const res = await bucketClient.listBuckets({
            parent: backendId ? backendParent(backendId) : "",
            page: { pageSize: API_PAGE_SIZE_MAX, pageToken: token },
            filter,
            ownerTenantId: ownerTenantId ?? "",
          });
          acc.push(...res.buckets);
          token = res.page?.nextPageToken ?? "";
          pages += 1;
        } while (token && pages < MAX_LIST_PAGES);
        setBuckets(acc);
        return {
          buckets: acc,
          nextPageToken: token,
        };
      } catch (err) {
        // Query contract (state-only): surface via `error`, never throw.
        setError(
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to fetch buckets",
        );
        return { buckets: [], nextPageToken: "" };
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const createBucket = useCallback(
    async (
      backendId: string,
      bucketId: string,
      displayName: string = "",
      region: string = "",
      provisionOnBackend: boolean = true,
    ): Promise<Bucket> => {
      try {
        setError(null);
        const bucket = create(BucketSchema, {
          name: bucketResourceName(backendId, bucketId),
          backendId,
          bucketId,
          displayName,
          region,
          ownerTenantId: "",
          cedarPolicy: "",
          lifecycleRules: [],
          labels: {},
          resourceVersion: "",
        });
        const created = await bucketClient.createBucket({
          parent: backendParent(backendId),
          bucketId,
          bucket,
          provisionOnBackend,
        });
        setBuckets((prev) => [...prev, created]);
        bumpRefresh("buckets");
        return created;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  const deleteBucket = useCallback(
    async (
      backendId: string,
      bucketId: string,
      resourceVersion: string = "",
      deleteOnBackend: boolean = false,
    ): Promise<void> => {
      try {
        setError(null);
        await bucketClient.deleteBucket({
          name: bucketResourceName(backendId, bucketId),
          resourceVersion,
          deleteOnBackend,
        });
        setBuckets((prev) =>
          prev.filter(
            (b) => !(b.backendId === backendId && b.bucketId === bucketId),
          ),
        );
        bumpRefresh("buckets");
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  // Update + lifecycle/policy/lock/replication setters are out of scope for
  // the current /buckets page; add them here when surfaces land.
  const updateBucket = useCallback(
    async (..._args: unknown[]): Promise<Bucket> => {
      throw new Error("updateBucket: not yet wired");
    },
    [],
  );

  return {
    buckets,
    loading,
    error,
    fetchBuckets,
    createBucket,
    updateBucket,
    deleteBucket,
  };
}
