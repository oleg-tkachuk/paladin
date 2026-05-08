"use client";

import { useCallback, useEffect, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";

import { objectClient, presignClient } from "@/lib/connect/client";
import type { Object$ } from "@/gen/paladin/data/v1/types_pb";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import type { PresignedUrl } from "@/gen/paladin/common/v1/resource_pb";
import { useAuth } from "@/context/AuthContext";
import { useRefreshSignal, useBumpRefresh } from "@/context/RefreshContext";
import { useNotification } from "@/components/ui/Notification";
import { DEFAULT_OBJECT_KEY } from "@/constants";

/**
 * useObject — singular variant for the inspector / detail page. Loads one
 * object via LookupObject (key + parent ObjectKey), refreshes its presigned
 * download URL when the object is AVAILABLE, and exposes per-object
 * mutators (soft-delete / restore / purge / patch-tags).
 *
 * Parent ObjectKey is assembled from the user's tenantId so callers only
 * pass the bare ObjectKey id (typically from URL or scope).
 */

export function useObject(
  key: string | undefined,
  parentObjectKey: string = DEFAULT_OBJECT_KEY,
) {
  const { user } = useAuth();
  const tenantId = user?.tenantId ?? "";
  const parent = tenantId
    ? `tenants/${tenantId}/objectKeys/${parentObjectKey}`
    : "";

  const { showNotification } = useNotification();
  const refreshSignal = useRefreshSignal("objects");
  const bumpRefresh = useBumpRefresh();

  const [object, setObject] = useState<Object$ | null>(null);
  const [downloadUrl, setDownloadUrl] = useState<PresignedUrl | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const fetchObject = useCallback(async () => {
    if (!key || !parent) return null;
    setLoading(true);
    setError(null);
    try {
      const obj = await objectClient.lookupObject({ parent, key });
      setObject(obj);

      if (obj.state === ObjectState.AVAILABLE) {
        try {
          const res = await presignClient.presignDownload({
            name: obj.name,
            contentDisposition: "",
          });
          setDownloadUrl(res.downloadUrl ?? null);
        } catch (downloadErr: unknown) {
          console.error("Failed to fetch download URL", downloadErr);
          setDownloadUrl(null);
        }
      } else {
        setDownloadUrl(null);
      }
      return obj;
    } catch (err: unknown) {
      const e = err as Error;
      setError(e);
      // NotFound is a normal state for the inspector — the row may have
      // been deleted in another tab or the user just changed scope. Show
      // it inline (`object` stays null) instead of a red toast.
      const isNotFound =
        err instanceof ConnectError && err.code === Code.NotFound;
      if (!isNotFound) {
        showNotification({
          type: "error",
          title: "Sync Failed",
          message: e.message || "Could not synchronize object details.",
        });
      }
      return null;
    } finally {
      setLoading(false);
    }
  }, [key, parent, showNotification]);

  const patchObjectMeta = useCallback(
    async (tags: Record<string, string>) => {
      if (!object) return;
      try {
        setLoading(true);
        await objectClient.updateObject({
          name: object.name,
          resourceVersion: object.resourceVersion,
          updateMask: create(FieldMaskSchema, { paths: ["tags"] }),
          tags,
          metadata: {},
          contentType: "",
          externalRef: "",
        });
        await fetchObject();
        showNotification({
          type: "success",
          title: "Update Successful",
          message: "Object metadata has been synchronized.",
        });
      } catch (err: unknown) {
        const e = err as Error;
        showNotification({
          type: "error",
          title: "Update Failed",
          message: e.message || "Could not update object metadata.",
        });
        throw e;
      } finally {
        setLoading(false);
      }
    },
    [object, fetchObject, showNotification],
  );

  const softDeleteObject = useCallback(async () => {
    if (!object) return;
    try {
      setLoading(true);
      await objectClient.deleteObject({
        name: object.name,
        resourceVersion: object.resourceVersion,
        permanent: false,
        bypassGovernanceRetention: false,
      });
      await fetchObject();
      bumpRefresh("objects");
      showNotification({
        type: "success",
        title: "Object Trashed",
        message: "Object has been moved to the trash bin.",
      });
    } catch (err: unknown) {
      showNotification({
        type: "error",
        title: "Action Failed",
        message: (err as Error).message,
      });
      throw err;
    } finally {
      setLoading(false);
    }
  }, [object, fetchObject, showNotification]);

  const purgeObject = useCallback(async () => {
    if (!object) return;
    try {
      setLoading(true);
      await objectClient.deleteObject({
        name: object.name,
        resourceVersion: object.resourceVersion,
        permanent: true,
        bypassGovernanceRetention: false,
      });
      await fetchObject();
      bumpRefresh("objects");
      showNotification({
        type: "success",
        title: "Object Purged",
        message: "Object has been permanently deleted.",
      });
    } catch (err: unknown) {
      showNotification({
        type: "error",
        title: "Action Failed",
        message: (err as Error).message,
      });
      throw err;
    } finally {
      setLoading(false);
    }
  }, [object, fetchObject, showNotification]);

  const restoreObject = useCallback(async () => {
    if (!object) return;
    try {
      setLoading(true);
      await objectClient.restoreObject({
        name: object.name,
        resourceVersion: object.resourceVersion,
      });
      await fetchObject();
      bumpRefresh("objects");
      showNotification({
        type: "success",
        title: "Object Restored",
        message: "Object has been recovered from the trash bin.",
      });
    } catch (err: unknown) {
      showNotification({
        type: "error",
        title: "Restore Failed",
        message: (err as Error).message,
      });
      throw err;
    } finally {
      setLoading(false);
    }
  }, [object, fetchObject, showNotification]);

  useEffect(() => {
    if (key) {
      void fetchObject();
    }
  }, [fetchObject, key, refreshSignal]);

  return {
    object,
    downloadUrl,
    loading,
    error,
    refresh: fetchObject,
    patchObjectMeta,
    softDeleteObject,
    restoreObject,
    purgeObject,
  };
}
