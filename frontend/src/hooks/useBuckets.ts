"use client";

import { useCallback, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";

import { bucketClient } from "@/lib/connect/client";
import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import {
  BucketConstraintsSchema,
  BucketSchema,
} from "@/gen/paladin/admin/v1/types_pb";
import { useBumpRefresh } from "@/context/RefreshContext";
import { errorMessage } from "@/hooks/errorContract";
import {
  BUCKETS_QUERY_KEY,
  bucketsQuery,
  type BucketRead,
} from "@/lib/queries/buckets";

// Ceiling on how many pages one fetchBuckets call will follow. At the maximum
// page size this is 10k buckets — far past any console list that stays usable,
// and a backstop against paging forever if a token ever fails to terminate.
export { MAX_LIST_PAGES } from "@/lib/queries/paging";
export type { BucketRead, BucketTenantScope } from "@/lib/queries/buckets";

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

/**
 * What makes a bucket public (ADR-0027): anyone reads its objects by URL.
 * Only on a backend whose probe found anonymous reads enforced.
 */
export interface PublicReadSettings {
  /** The content types the bucket serves; none a browser would execute. */
  allowedContentTypes: string[];
  /** Where a CDN serves the bucket; "" for the backend's public endpoint. */
  baseUrl: string;
}

/**
 * The buckets a page reads, through the query cache (lib/queries/buckets),
 * and the changes to them. null holds the read, as a dialog does until it
 * opens. A create or a delete settles in the reconciler, not in the RPC, so
 * neither is predicted: every buckets list reads again after one.
 */
export function useBuckets(read: BucketRead | null = {}) {
  const bumpRefresh = useBumpRefresh();
  const qc = useQueryClient();
  const query = useQuery({
    ...bucketsQuery(read ?? {}),
    enabled: read !== null,
    // A new search keeps the last result on screen until it answers.
    placeholderData: (previous) => previous,
  });
  const buckets = useMemo(() => query.data ?? [], [query.data]);
  const error = query.error
    ? errorMessage(query.error, "Failed to fetch buckets")
    : null;
  const { refetch: refetchQuery } = query;
  const refetch = useCallback(() => void refetchQuery(), [refetchQuery]);

  const settle = useCallback(() => {
    void qc.invalidateQueries({ queryKey: BUCKETS_QUERY_KEY });
    bumpRefresh("buckets");
  }, [qc, bumpRefresh]);

  const createBucket = useCallback(
    async (
      backendId: string,
      bucketId: string,
      displayName: string = "",
      region: string = "",
      provisionOnBackend: boolean = true,
      publicRead: PublicReadSettings | null,
      // ADR-0032: every bucket has an owner tenant, by UUID; the server
      // refuses an empty one. Other tenants bind to it only through a grant.
      ownerTenantId: string,
    ): Promise<Bucket> => {
      try {
        const bucket = create(BucketSchema, {
          name: bucketResourceName(backendId, bucketId),
          backendId,
          bucketId,
          displayName,
          region,
          ownerTenantId,
          cedarPolicy: "",
          lifecycleRules: [],
          labels: {},
          resourceVersion: "",
          // ADR-0027: fixed at creation. A public bucket lists the content
          // types it serves; the server refuses one that does not.
          publicRead: publicRead !== null,
          publicBaseUrl: publicRead?.baseUrl ?? "",
          constraints: publicRead
            ? create(BucketConstraintsSchema, {
                allowedContentTypes: publicRead.allowedContentTypes,
              })
            : undefined,
        });
        const created = await bucketClient.createBucket({
          parent: backendParent(backendId),
          bucketId,
          bucket,
          provisionOnBackend,
        });
        settle();
        return created;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [settle],
  );

  const deleteBucket = useCallback(
    async (
      backendId: string,
      bucketId: string,
      resourceVersion: string = "",
      deleteOnBackend: boolean = false,
    ): Promise<void> => {
      try {
        await bucketClient.deleteBucket({
          name: bucketResourceName(backendId, bucketId),
          resourceVersion,
          deleteOnBackend,
        });
        settle();
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [settle],
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
    loading: query.isFetching,
    error,
    refetch,
    /** The read has answered at least once, for a default chosen from it. */
    fetched: query.isFetched,
    createBucket,
    updateBucket,
    deleteBucket,
  };
}
