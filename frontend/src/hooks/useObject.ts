"use client";

import { useCallback } from "react";
import { useQuery } from "@tanstack/react-query";
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
import { normalizeError } from "@/lib/connect/error";
import { DEFAULT_OBJECT_KEY } from "@/constants";

/**
 * useObject — singular variant for the inspector / detail page. Loads one
 * object via LookupObject (key + parent Collection) and, when AVAILABLE, its
 * presigned download URL — both in one TanStack query. Exposes per-object
 * mutators (soft-delete / restore / purge / patch-tags) that refetch the
 * query and bump the shared "objects" signal so list views stay in sync.
 */

interface ObjectQueryResult {
  object: Object$;
  downloadUrl: PresignedUrl | null;
}

export function useObject(
  key: string | undefined,
  parentCollection: string = DEFAULT_OBJECT_KEY,
) {
  const { user } = useAuth();
  const tenantId = user?.tenantId ?? "";
  const parent = tenantId
    ? `tenants/${tenantId}/collections/${parentCollection}`
    : "";

  const { showNotification } = useNotification();
  const refreshSignal = useRefreshSignal("objects");
  const bumpRefresh = useBumpRefresh();

  const query = useQuery<ObjectQueryResult>({
    // refreshSignal in the key keeps the old refetch-on-bump behaviour.
    queryKey: ["object", parent, key, refreshSignal],
    enabled: !!key && !!parent,
    // No retry: the queryFn toasts non-NotFound failures, and a retry would
    // double-toast (and NotFound is a normal inspector state, not transient).
    retry: false,
    queryFn: async ({ signal }) => {
      try {
        const obj = await objectClient.lookupObject(
          { parent, key: key! },
          { signal },
        );
        let downloadUrl: PresignedUrl | null = null;
        if (obj.state === ObjectState.AVAILABLE) {
          try {
            const res = await presignClient.presignDownload(
              { name: obj.name, contentDisposition: "" },
              { signal },
            );
            downloadUrl = res.downloadUrl ?? null;
          } catch (downloadErr: unknown) {
            // Non-fatal: detail page still renders without the download link.
            console.error("Failed to fetch download URL", downloadErr);
          }
        }
        return { object: obj, downloadUrl };
      } catch (err: unknown) {
        // NotFound is a normal inspector state (row deleted in another tab,
        // or scope just changed) — show it inline, no red toast. Everything
        // else is a genuine sync failure worth surfacing.
        const isNotFound =
          err instanceof ConnectError && err.code === Code.NotFound;
        if (!isNotFound) {
          showNotification({
            type: "error",
            title: "Sync Failed",
            message:
              err instanceof Error
                ? err.message
                : "Could not synchronize object details.",
          });
        }
        throw normalizeError(err);
      }
    },
  });

  const object = query.data?.object ?? null;
  const downloadUrl = query.data?.downloadUrl ?? null;

  // refetch is stable; query is a new object on every render.
  const { refetch } = query;
  const refresh = useCallback(async () => {
    await refetch();
  }, [refetch]);

  // Mutators: run the RPC, refetch this object, bump the shared signal so
  // list views refetch too, then toast. The previous hand-rolled loading
  // flag is replaced by query.isFetching during the refetch.
  const patchObjectMeta = useCallback(
    async (tags: Record<string, string>) => {
      if (!object) return;
      try {
        await objectClient.updateObject({
          name: object.name,
          resourceVersion: object.resourceVersion,
          updateMask: create(FieldMaskSchema, { paths: ["tags"] }),
          tags,
          metadata: {},
          contentType: "",
          externalRef: "",
        });
        await refresh();
        bumpRefresh("objects");
        showNotification({
          type: "success",
          title: "Update Successful",
          message: "Object metadata has been synchronized.",
        });
      } catch (err: unknown) {
        showNotification({
          type: "error",
          title: "Update Failed",
          message:
            (err as Error).message || "Could not update object metadata.",
        });
        throw err;
      }
    },
    [object, refresh, bumpRefresh, showNotification],
  );

  const softDeleteObject = useCallback(async () => {
    if (!object) return;
    try {
      await objectClient.deleteObject({
        name: object.name,
        resourceVersion: object.resourceVersion,
        permanent: false,
        bypassGovernanceRetention: false,
      });
      await refresh();
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
    }
  }, [object, refresh, bumpRefresh, showNotification]);

  const purgeObject = useCallback(async () => {
    if (!object) return;
    try {
      await objectClient.deleteObject({
        name: object.name,
        resourceVersion: object.resourceVersion,
        permanent: true,
        bypassGovernanceRetention: false,
      });
      await refresh();
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
    }
  }, [object, refresh, bumpRefresh, showNotification]);

  const restoreObject = useCallback(async () => {
    if (!object) return;
    try {
      await objectClient.restoreObject({
        name: object.name,
        resourceVersion: object.resourceVersion,
      });
      await refresh();
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
    }
  }, [object, refresh, bumpRefresh, showNotification]);

  return {
    object,
    downloadUrl,
    loading: query.isFetching,
    error: (query.error as Error | null) ?? null,
    refresh,
    patchObjectMeta,
    softDeleteObject,
    restoreObject,
    purgeObject,
  };
}
