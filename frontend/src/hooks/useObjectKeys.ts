"use client";

import { useCallback, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";
import { ConnectError } from "@connectrpc/connect";

import { objectKeyClient } from "@/lib/connect/client";
import type { ObjectKey } from "@/gen/paladin/admin/v1/types_pb";
import { ObjectKeySchema } from "@/gen/paladin/admin/v1/types_pb";
import { useAuth } from "@/context/AuthContext";
import { useBumpRefresh } from "@/context/RefreshContext";
import { API_PAGE_SIZE_MAX } from "@/constants";

/**
 * useObjectKeys — wrapper around admin/v1.ObjectKeyService.
 *
 * Resource names:
 *   ObjectKey → "tenants/{tenant_id}/objectKeys/{object_key}"
 *   Bucket    → "storageBackends/{backend_id}/buckets/{bucket_name}"
 *
 * The new schema replaced flat `policy`/`backendId`/`bucketName` fields
 * with a nested `objectKeyResource` carrying `cedarPolicy: string` and
 * `bucket: string` (full resource name). Page-level callers still use the
 * legacy positional signatures — the hook builds the new request shapes.
 *
 * `parent` is sourced from the signed-in user's tenantId so the page
 * doesn't need to thread it through.
 */

const objectKeyResourceName = (tenantId: string, objectKey: string) =>
  `tenants/${tenantId}/objectKeys/${objectKey}`;

const bucketResourceName = (backendId: string, bucketName: string) =>
  `storageBackends/${backendId}/buckets/${bucketName}`;

export function useObjectKeys() {
  const { user } = useAuth();
  const tenantParent = user?.tenantId ? `tenants/${user.tenantId}` : "";
  const bumpRefresh = useBumpRefresh();

  const [objectKeys, setObjectKeys] = useState<ObjectKey[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pageTokens, setPageTokens] = useState<string[]>([""]);

  const fetchObjectKeys = useCallback(
    async (filter: string = "", pageToken: string = "") => {
      setLoading(true);
      setError(null);
      try {
        const res = await objectKeyClient.listObjectKeys({
          parent: tenantParent,
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken },
          filter,
        });
        setObjectKeys(res.objectKeys);
        return {
          objectKeys: res.objectKeys,
          nextPageToken: res.page?.nextPageToken ?? "",
        };
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to fetch object keys";
        setError(msg);
        throw err;
      } finally {
        setLoading(false);
      }
    },
    [tenantParent],
  );

  const createObjectKey = useCallback(
    async (
      objectKey: string,
      displayName: string = "",
      backendId: string = "",
      bucketName: string = "",
      cedarPolicy: string = "",
    ): Promise<ObjectKey> => {
      try {
        setError(null);
        const tenantId = user?.tenantId ?? "";
        const objectKeyResource = create(ObjectKeySchema, {
          name: objectKeyResourceName(tenantId, objectKey),
          tenantId,
          objectKey,
          displayName,
          bucket:
            backendId && bucketName
              ? bucketResourceName(backendId, bucketName)
              : "",
          cedarPolicy,
          resourceVersion: "",
        });
        const created = await objectKeyClient.createObjectKey({
          parent: tenantParent,
          objectKey,
          objectKeyResource,
        });
        setObjectKeys((prev) => [...prev, created]);
        bumpRefresh("objectKeys");
        return created;
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to create object key";
        setError(msg);
        throw err;
      }
    },
    [tenantParent, user?.tenantId],
  );

  const deleteObjectKey = useCallback(
    async (
      objectKey: string,
      resourceVersion: string = "",
      force: boolean = false,
    ): Promise<void> => {
      try {
        setError(null);
        const tenantId = user?.tenantId ?? "";
        await objectKeyClient.deleteObjectKey({
          name: objectKeyResourceName(tenantId, objectKey),
          resourceVersion,
          force,
        });
        setObjectKeys((prev) => prev.filter((b) => b.objectKey !== objectKey));
        // Deleting an ObjectKey may have removed (or trashed) every object
        // beneath it — fan out to "objects" so any open list refetches.
        bumpRefresh(["objectKeys", "objects"]);
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to delete object key";
        setError(msg);
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
  const getObjectKey = useCallback(
    async (objectKey: string): Promise<ObjectKey | null> => {
      try {
        setError(null);
        return await objectKeyClient.getObjectKey({
          name: objectKeyResourceName(callerTenantId, objectKey),
        });
      } catch (err) {
        if (err instanceof ConnectError) setError(err.rawMessage);
        throw err;
      }
    },
    [callerTenantId],
  );

  const updateObjectKey = useCallback(
    async (
      objectKey: string,
      resourceVersion: string,
      updatePaths: string[],
      fields: { displayName?: string; cedarPolicy?: string } = {},
    ): Promise<ObjectKey> => {
      try {
        setError(null);
        const tenantId = user?.tenantId ?? "";
        const objectKeyResource = create(ObjectKeySchema, {
          name: objectKeyResourceName(tenantId, objectKey),
          tenantId,
          objectKey,
          displayName: fields.displayName ?? "",
          cedarPolicy: fields.cedarPolicy ?? "",
          resourceVersion,
        });
        const updated = await objectKeyClient.updateObjectKey({
          name: objectKeyResourceName(tenantId, objectKey),
          resourceVersion,
          updateMask: create(FieldMaskSchema, { paths: updatePaths }),
          objectKeyResource,
        });
        setObjectKeys((prev) =>
          prev.map((b) => (b.objectKey === objectKey ? updated : b)),
        );
        bumpRefresh("objectKeys");
        return updated;
      } catch (err) {
        if (err instanceof ConnectError) setError(err.rawMessage);
        throw err;
      }
    },
    [user?.tenantId],
  );

  // Stats RPC was removed during the proto refactor. Surface a stable shape
  // returning zeros so consumers (object-key detail page) keep compiling;
  // the dashboard tile that used it is now blank rather than wrong.
  const getObjectKeyStats = useCallback(
    async (
      _objectKey: string,
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
    objectKeys,
    loading,
    error,
    fetchObjectKeys,
    createObjectKey,
    deleteObjectKey,
    getObjectKey,
    updateObjectKey,
    getObjectKeyStats,
    pageTokens,
    setPageTokens,
  };
}
