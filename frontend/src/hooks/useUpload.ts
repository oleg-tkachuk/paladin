"use client";

import { useCallback, useState } from "react";

import {
  MULTIPART_THRESHOLD_BYTES,
  uploadMultipart,
  uploadSingle,
} from "@/lib/upload/presigned";
import { useAuth } from "@/context/AuthContext";
import { useBumpRefresh } from "@/context/RefreshContext";
import { useNotification } from "@/components/ui/Notification";
import { errorMessage } from "@/hooks/errorContract";

/**
 * useUpload — three-step PUT-presign flow (see lib/upload/presigned):
 *   1. UploadObject → server allocates an Object row + a presigned PUT URL
 *      bound to the file's size and SHA-256.
 *   2. PUT file body to that URL (origin-direct, no BFF in the middle).
 *   3. CompleteObject → server flips the Object to AVAILABLE.
 *
 * The hook keeps a queue of in-flight uploads with progress so the page
 * can render a list of cards. Tags + content-type are passed through to
 * UploadObject; per-file overrides aren't supported yet — rare enough
 * that the existing UI didn't expose them either.
 */

export type UploadStatus = "pending" | "uploading" | "completed" | "error";

export type UploadQueueItem = {
  id: string;
  name: string;
  size: number;
  /** 0..100 */
  progress: number;
  status: UploadStatus;
  error?: string;
  file: File;
  /** Backend resource name once UploadObject + CompleteObject have run. */
  objectName?: string;
  [key: string]: unknown;
};

// Legacy alias kept so /upload page imports still resolve.
export type UploadTask = UploadQueueItem;

export function useUpload() {
  const { user } = useAuth();
  const tenantId = user?.tenantId ?? "";
  const { showNotification } = useNotification();
  const bumpRefresh = useBumpRefresh();

  const [queue, setQueue] = useState<UploadQueueItem[]>([]);

  const update = (id: string, patch: Partial<UploadQueueItem>) =>
    setQueue((prev) =>
      prev.map((item) => (item.id === id ? { ...item, ...patch } : item)),
    );

  const uploadFile = useCallback(
    async (
      file: File,
      parentCollection: string,
      tags: Record<string, string> = {},
    ): Promise<void> => {
      const id = `${file.name}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
      const initial: UploadQueueItem = {
        id,
        name: file.name,
        size: file.size,
        progress: 0,
        status: "pending",
        file,
      };
      setQueue((prev) => [initial, ...prev]);

      if (!tenantId) {
        update(id, { status: "error", error: "no tenant on session" });
        return;
      }

      try {
        update(id, { status: "uploading" });
        const parent = `tenants/${tenantId}/collections/${parentCollection}`;

        // Above the threshold the single-shot path cannot help: one presigned
        // PUT means one request, one timeout, and one failure that costs the
        // whole transfer. Multipart splits it, and is the only route for a
        // file over the backend's max_object_size.
        if (file.size > MULTIPART_THRESHOLD_BYTES) {
          const objectName = await uploadMultipart({
            parent,
            file,
            tags,
            onProgress: (pct) => update(id, { progress: pct }),
          });
          update(id, { status: "completed", progress: 100, objectName });
          bumpRefresh("objects");
          showNotification({
            type: "success",
            title: "Upload complete",
            message: `${file.name} uploaded in parts.`,
          });
          return;
        }

        const objectName = await uploadSingle({
          parent,
          file,
          tags,
          idempotencyKey: id,
          onProgress: (pct) => update(id, { progress: pct }),
        });

        update(id, { status: "completed", progress: 100, objectName });
        bumpRefresh("objects");
        showNotification({
          type: "success",
          title: "Upload complete",
          message: file.name,
        });
      } catch (err: unknown) {
        const msg = errorMessage(err, "Upload failed");
        update(id, { status: "error", error: msg });
        showNotification({
          type: "error",
          title: "Upload failed",
          message: `${file.name}: ${msg}`,
        });
      }
    },
    [tenantId, showNotification, bumpRefresh],
  );

  const clearQueue = useCallback(() => setQueue([]), []);

  return { queue, uploadFile, clearQueue };
}
