"use client";

import { useCallback, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";
import { ConnectError } from "@connectrpc/connect";

import { collectionClient } from "@/lib/connect/client";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { CollectionSchema } from "@/gen/paladin/admin/v1/types_pb";
import { useAuth } from "@/context/AuthContext";
import { useBumpRefresh } from "@/context/RefreshContext";
import { API_PAGE_SIZE_MAX } from "@/constants";

/**
 * useCollections — wrapper around admin/v1.CollectionService.
 *
 * Resource names:
 *   Collection → "tenants/{tenant_id}/collections/{collection}"
 *   Bucket    → "storageBackends/{backend_id}/buckets/{bucket_id}"
 *
 * The new schema replaced flat `policy`/`backendId`/`bucketId` fields
 * with a nested `collectionResource` carrying `cedarPolicy: string` and
 * `bucket: string` (full resource name). Page-level callers still use the
 * legacy positional signatures — the hook builds the new request shapes.
 *
 * `parent` is sourced from the signed-in user's tenantId so the page
 * doesn't need to thread it through.
 */

const collectionResourceName = (tenantId: string, collection: string) =>
  `tenants/${tenantId}/collections/${collection}`;

const bucketResourceName = (backendId: string, bucketId: string) =>
  `storageBackends/${backendId}/buckets/${bucketId}`;

export function useCollections() {
  const { user } = useAuth();
  const tenantParent = user?.tenantId ? `tenants/${user.tenantId}` : "";
  const bumpRefresh = useBumpRefresh();

  const [collections, setCollections] = useState<Collection[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pageTokens, setPageTokens] = useState<string[]>([""]);

  const fetchCollections = useCallback(
    async (filter: string = "", pageToken: string = "") => {
      setLoading(true);
      setError(null);
      try {
        const res = await collectionClient.listCollections({
          parent: tenantParent,
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken },
          filter,
        });
        setCollections(res.collections);
        return {
          collections: res.collections,
          nextPageToken: res.page?.nextPageToken ?? "",
        };
      } catch (err) {
        // Query contract (state-only): surface via `error`, never throw.
        setError(
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to fetch object keys",
        );
        return { collections: [], nextPageToken: "" };
      } finally {
        setLoading(false);
      }
    },
    [tenantParent],
  );

  const createCollection = useCallback(
    async (
      collection: string,
      displayName: string = "",
      backendId: string = "",
      bucketId: string = "",
      cedarPolicy: string = "",
    ): Promise<Collection> => {
      try {
        setError(null);
        const tenantId = user?.tenantId ?? "";
        const collectionResource = create(CollectionSchema, {
          name: collectionResourceName(tenantId, collection),
          tenantId,
          collection,
          displayName,
          bucket:
            backendId && bucketId
              ? bucketResourceName(backendId, bucketId)
              : "",
          cedarPolicy,
          resourceVersion: "",
        });
        const created = await collectionClient.createCollection({
          parent: tenantParent,
          collection,
          collectionResource,
        });
        setCollections((prev) => [...prev, created]);
        bumpRefresh("collections");
        return created;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [tenantParent, user?.tenantId],
  );

  const deleteCollection = useCallback(
    async (
      collection: string,
      resourceVersion: string = "",
      force: boolean = false,
    ): Promise<void> => {
      try {
        setError(null);
        const tenantId = user?.tenantId ?? "";
        await collectionClient.deleteCollection({
          name: collectionResourceName(tenantId, collection),
          resourceVersion,
          force,
        });
        setCollections((prev) =>
          prev.filter((b) => b.collection !== collection),
        );
        // Deleting an Collection may have removed (or trashed) every object
        // beneath it — fan out to "objects" so any open list refetches.
        bumpRefresh(["collections", "objects"]);
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [user?.tenantId],
  );

  // Hoist the tenant id so the callback closes over a scalar, not `user`.
  // React Compiler infers the dep from the body; referencing `user?.tenantId`
  // inline made the inferred dep (`user`) disagree with the manual
  // `[user?.tenantId]` (react-hooks/preserve-manual-memoization).
  const callerTenantId = user?.tenantId ?? "";
  const getCollection = useCallback(
    async (collection: string): Promise<Collection | null> => {
      try {
        setError(null);
        return await collectionClient.getCollection({
          name: collectionResourceName(callerTenantId, collection),
        });
      } catch (err) {
        // Imperative read (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [callerTenantId],
  );

  const updateCollection = useCallback(
    async (
      collection: string,
      resourceVersion: string,
      updatePaths: string[],
      fields: { displayName?: string; cedarPolicy?: string } = {},
    ): Promise<Collection> => {
      try {
        setError(null);
        const tenantId = user?.tenantId ?? "";
        const collectionResource = create(CollectionSchema, {
          name: collectionResourceName(tenantId, collection),
          tenantId,
          collection,
          displayName: fields.displayName ?? "",
          cedarPolicy: fields.cedarPolicy ?? "",
          resourceVersion,
        });
        const updated = await collectionClient.updateCollection({
          name: collectionResourceName(tenantId, collection),
          resourceVersion,
          updateMask: create(FieldMaskSchema, { paths: updatePaths }),
          collectionResource,
        });
        setCollections((prev) =>
          prev.map((b) => (b.collection === collection ? updated : b)),
        );
        bumpRefresh("collections");
        return updated;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [user?.tenantId],
  );

  // Stats RPC was removed during the proto refactor. Surface a stable shape
  // returning zeros so consumers (object-key detail page) keep compiling;
  // the dashboard tile that used it is now blank rather than wrong.
  const getCollectionStats = useCallback(
    async (
      _collection: string,
    ): Promise<{
      totalObjects: number;
      totalSizeBytes: number;
      countByState: Record<string, number>;
    }> => ({
      totalObjects: 0,
      totalSizeBytes: 0,
      countByState: {},
    }),
    [],
  );

  return {
    collections,
    loading,
    error,
    fetchCollections,
    createCollection,
    deleteCollection,
    getCollection,
    updateCollection,
    getCollectionStats,
    pageTokens,
    setPageTokens,
  };
}
