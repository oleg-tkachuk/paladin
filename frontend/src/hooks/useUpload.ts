"use client";

import { useCallback, useState } from "react";

import { objectClient, multipartClient } from "@/lib/connect/client";
import { ChecksumAlgorithm } from "@/gen/paladin/common/v1/resource_pb";
import { PresignTransport } from "@/gen/paladin/data/v1/object_service_pb";
import { useAuth } from "@/context/AuthContext";
import { useBumpRefresh } from "@/context/RefreshContext";
import { useNotification } from "@/components/ui/Notification";
import { errorMessage } from "@/hooks/errorContract";

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

/**
 * Files at or below this go through the single-shot presigned PUT: one
 * request, no session to abandon if the tab closes. Above it, multipart —
 * which is also the only way past the backend's max_object_size.
 *
 * 8 MiB is the smallest part S3 semantics allow us to rely on (the 5 MiB
 * floor applies to every part but the last), with headroom.
 */
const MULTIPART_THRESHOLD_BYTES = 8 * 1024 * 1024;

/** Concurrent part PUTs — enough to use the link, few enough that one
 *  failure does not cost several parts at once. */
const PART_CONCURRENCY = 3;

/**
 * Runs Initiate → (PresignPart → PUT) × N → Complete and returns the
 * object's resource name.
 *
 * Each part's ETag comes back only from its PUT response, and Complete
 * needs all of them — the backend cannot reconstruct them, so a dropped
 * ETag loses the upload. Browsers expose that header only when the backend
 * lists it in Access-Control-Expose-Headers, which is why a missing one is
 * reported as configuration rather than as a transfer error.
 *
 * On failure the session is aborted: the parts already stored are released
 * rather than left to bill as storage until the reaper notices.
 */
async function uploadMultipart(args: {
  parent: string;
  file: File;
  tags: Record<string, string>;
  onProgress: (pct: number) => void;
}): Promise<string> {
  const { parent, file } = args;
  const init = await multipartClient.initiateMultipartUpload({
    parent,
    key: file.name,
    contentType: file.type || "application/octet-stream",
    sizeBytes: BigInt(file.size),
    tags: args.tags,
    // Required: buf-validate rejects UNSPECIFIED here, same as the
    // single-shot path. SHA256 is what the data plane verifies on complete.
    checksumAlgorithm: ChecksumAlgorithm.SHA256,
  });
  const objectName = init.object?.name ?? "";
  if (!objectName || !init.uploadId) {
    throw new Error("InitiateMultipartUpload returned no session");
  }

  // The server picks the part size; it knows the backend's limits, and
  // trusting it keeps total_parts consistent with what Complete verifies.
  const partSize = Number(init.recommendedPartSize);
  const partsTotal =
    init.totalParts || Math.max(1, Math.ceil(file.size / partSize));
  const etags = new Array<string>(partsTotal);

  try {
    let bytesSent = 0;
    let next = 0;
    const worker = async () => {
      for (;;) {
        const index = next++;
        if (index >= partsTotal) return;
        const partNumber = index + 1; // S3 part numbers are 1-based
        const start = index * partSize;
        const slice = file.slice(start, Math.min(start + partSize, file.size));

        const signed = await multipartClient.presignPart({
          objectName,
          uploadId: init.uploadId,
          partNumber,
        });
        const url = signed.uploadUrl;
        if (!url?.url) {
          throw new Error(`No presigned URL for part ${partNumber}`);
        }
        const res = await fetch(url.url, {
          method: url.method || "PUT",
          // required_headers are covered by the signature; omitting one
          // makes the backend reject the PUT as a signature mismatch,
          // which reads like a credentials problem.
          headers: { ...url.requiredHeaders },
          body: slice,
        });
        if (!res.ok) {
          throw new Error(
            `Part ${partNumber} failed: ${res.status} ${res.statusText}`,
          );
        }
        const etag = (res.headers.get("ETag") ?? "").replaceAll('"', "");
        if (!etag) {
          throw new Error(
            `Part ${partNumber} returned no ETag — the storage backend must ` +
              `list it in Access-Control-Expose-Headers for browser uploads`,
          );
        }
        etags[index] = etag;
        bytesSent += slice.size;
        args.onProgress(Math.round((bytesSent / file.size) * 100));
      }
    };
    await Promise.all(
      Array.from({ length: Math.min(PART_CONCURRENCY, partsTotal) }, worker),
    );

    await multipartClient.completeMultipartUpload({
      objectName,
      uploadId: init.uploadId,
      parts: etags.map((etag, i) => ({ partNumber: i + 1, etag })),
    });
    return objectName;
  } catch (e) {
    // Best-effort: a failed abort must not mask the error that caused it.
    try {
      await multipartClient.abortMultipartUpload({
        objectName,
        uploadId: init.uploadId,
      });
    } catch {
      /* the reaper sweeps abandoned sessions */
    }
    throw e;
  }
}

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
