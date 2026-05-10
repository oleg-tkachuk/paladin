"use client";

import { useCallback, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { ConnectError } from "@connectrpc/connect";

import { bucketClient } from "@/lib/connect/client";
import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { BucketSchema } from "@/gen/paladin/admin/v1/types_pb";
import { useBumpRefresh } from "@/context/RefreshContext";
import { API_PAGE_SIZE_MAX } from "@/constants";

/**
 * useBuckets — wrapper around admin/v1.BucketService.
 *
 * Resource name layout: `storageBackends/{backend_id}/buckets/{bucket_name}`.
 * Create takes `parent + bucket_name + bucket(resource)`; Update takes
 * `name + resource_version + update_mask + bucket(resource)`; Delete uses
 * `delete_on_backend` (renamed from the old `delete_remote`).
 *
 * Page-level callers pass legacy positional args (backendId, bucketName, …)
 * — the wrapper translates them into the nested request shapes so pages
 * don't need to know about the new schema.
 */

const backendParent = (backendId: string) => `storageBackends/${backendId}`;
const bucketResourceName = (backendId: string, bucketName: string) =>
  `storageBackends/${backendId}/buckets/${bucketName}`;

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
        // ownerTenantId pushes the tenant-narrow filter to the
        // server (uses the partial index on buckets.owner_tenant_id).
        // Empty/undefined preserves the cross-tenant listing for
        // platform-admin views.
        const res = await bucketClient.listBuckets({
          parent: backendId ? backendParent(backendId) : "",
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken },
          filter,
          ownerTenantId: ownerTenantId ?? "",
        });
        setBuckets(res.buckets);
        return {
          buckets: res.buckets,
          nextPageToken: res.page?.nextPageToken ?? "",
        };
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to fetch buckets";
        setError(msg);
        throw err;
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const createBucket = useCallback(
    async (
      backendId: string,
      bucketName: string,
      displayName: string = "",
      region: string = "",
      provisionOnBackend: boolean = true,
    ): Promise<Bucket> => {
      try {
        setError(null);
        const bucket = create(BucketSchema, {
          name: bucketResourceName(backendId, bucketName),
          backendId,
          bucketName,
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
          bucketName,
          bucket,
          provisionOnBackend,
        });
        setBuckets((prev) => [...prev, created]);
        bumpRefresh("buckets");
        return created;
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to create bucket";
        setError(msg);
        throw err;
      }
    },
    [],
  );

  const deleteBucket = useCallback(
    async (
      backendId: string,
      bucketName: string,
      resourceVersion: string = "",
      deleteOnBackend: boolean = false,
    ): Promise<void> => {
      try {
        setError(null);
        await bucketClient.deleteBucket({
          name: bucketResourceName(backendId, bucketName),
          resourceVersion,
          deleteOnBackend,
        });
        setBuckets((prev) =>
          prev.filter(
            (b) => !(b.backendId === backendId && b.bucketName === bucketName),
          ),
        );
        bumpRefresh("buckets");
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to delete bucket";
        setError(msg);
        throw err;
      }
    },
    [],
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
