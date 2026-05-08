"use client";

import { useCallback, useState } from "react";
import { ConnectError } from "@connectrpc/connect";

import { objectClient } from "@/lib/connect/client";
import { ChecksumAlgorithm } from "@/gen/paladin/common/v1/resource_pb";
import { PresignTransport } from "@/gen/paladin/data/v1/object_service_pb";
import { useAuth } from "@/context/AuthContext";
import { useBumpRefresh } from "@/context/RefreshContext";
import { useNotification } from "@/components/ui/Notification";

/**
 * useUpload — three-step PUT-presign flow:
 *   1. UploadObject → server allocates an Object row + presigned PUT URL.
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

function uploadXhr(
  url: string,
  file: File,
  contentType: string,
  onProgress: (pct: number) => void,
): Promise<string | undefined> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", url);
    if (contentType) xhr.setRequestHeader("Content-Type", contentType);
    xhr.upload.onprogress = (e) => {
      if (!e.lengthComputable) return;
      onProgress(Math.round((e.loaded / e.total) * 100));
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        // S3 returns the ETag as a quoted hex string in this header.
        const etag = xhr.getResponseHeader("ETag")?.replace(/"/g, "");
        resolve(etag ?? undefined);
      } else {
        reject(new Error(`PUT failed (${xhr.status}): ${xhr.responseText}`));
      }
    };
    xhr.onerror = () => reject(new Error("Network error during upload"));
    xhr.send(file);
  });
}

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
      parentObjectKey: string,
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
        const parent = `tenants/${tenantId}/objectKeys/${parentObjectKey}`;

        const allocated = await objectClient.uploadObject({
          parent,
          key: file.name,
          contentType: file.type || "application/octet-stream",
          sizeHintBytes: BigInt(file.size),
          // Backend's buf-validate rule rejects UNSPECIFIED on this
          // field. SHA256 is the spec-default and what the data plane
          // verifies on completion; CRC32C is also accepted but slower
          // to compute in pure JS.
          checksumAlgorithm: ChecksumAlgorithm.SHA256,
          metadata: {},
          tags,
          externalRef: "",
          transport: PresignTransport.PUT,
          idempotencyKey: id,
        });
        if (!allocated.uploadUrl?.url || !allocated.object) {
          throw new Error("UploadObject returned no upload URL");
        }

        const etag = await uploadXhr(
          allocated.uploadUrl.url,
          file,
          file.type,
          (pct) => update(id, { progress: pct }),
        );

        await objectClient.completeObject({
          name: allocated.object.name,
          etag: etag ?? "",
          checksumValue: "",
        });

        update(id, {
          status: "completed",
          progress: 100,
          objectName: allocated.object.name,
        });
        bumpRefresh("objects");
        showNotification({
          type: "success",
          title: "Upload complete",
          message: file.name,
        });
      } catch (err: unknown) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : (err as Error).message || "Upload failed";
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
