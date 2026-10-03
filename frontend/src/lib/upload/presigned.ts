import { objectClient, multipartClient } from "@/lib/connect/client";
import { ChecksumAlgorithm } from "@/gen/paladin/common/v1/resource_pb";
import { PresignTransport } from "@/gen/paladin/data/v1/object_service_pb";

/**
 * Files at or below this go through the single-shot presigned PUT: one
 * request, no session to abandon if the tab closes. Above it, multipart.
 *
 * 8 MiB is the smallest part S3 semantics allow us to rely on (the 5 MiB
 * floor applies to every part but the last), with headroom. The whole file
 * is hashed in memory before it is presigned, so the threshold also bounds
 * what a single upload holds.
 */
export const MULTIPART_THRESHOLD_BYTES = 8 * 1024 * 1024;

/** Concurrent part PUTs — enough to use the link, few enough that one
 *  failure does not cost several parts at once. */
export const PART_CONCURRENCY = 3;

/** What an object is uploaded as when the browser gives no type. */
export const DEFAULT_CONTENT_TYPE = "application/octet-stream";

/**
 * Headers a browser sets itself and refuses from script: the signature
 * covers them, and the browser's own values are the signed ones — Host is
 * the URL's, Content-Length the body's.
 */
const BROWSER_OWNED_HEADERS = new Set(["host", "content-length"]);

/**
 * The headers a presigned request must carry: every signed header, minus the
 * ones the browser owns. Omitting a signed header makes storage reject the
 * request as a signature mismatch, which reads like a credentials problem.
 */
export function signedRequestHeaders(
  required: Record<string, string> | undefined,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [name, value] of Object.entries(required ?? {})) {
    if (!BROWSER_OWNED_HEADERS.has(name.toLowerCase())) out[name] = value;
  }
  return out;
}

/**
 * The SHA-256 of a blob as an upload's checksum_value: base64 of the digest,
 * as S3 writes it. The upload URL is signed for it, so it must be computed
 * over exactly the bytes the URL will be sent.
 */
export async function sha256Base64(blob: Blob): Promise<string> {
  const digest = await crypto.subtle.digest(
    "SHA-256",
    await blob.arrayBuffer(),
  );
  let binary = "";
  for (const byte of new Uint8Array(digest))
    binary += String.fromCharCode(byte);
  return btoa(binary);
}

type Progress = (pct: number) => void;

/** PUTs a body through XHR, for its upload progress; resolves to the ETag. */
function putWithProgress(
  method: string,
  url: string,
  headers: Record<string, string>,
  body: Blob,
  onProgress: Progress,
): Promise<string | undefined> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open(method, url);
    for (const [name, value] of Object.entries(headers)) {
      xhr.setRequestHeader(name, value);
    }
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
    xhr.send(body);
  });
}

/**
 * UploadObject → PUT → CompleteObject. The URL is signed for the file's
 * size, Content-Type and SHA-256, so the file is hashed first and the PUT
 * carries every signed header — including If-None-Match: *, which keeps it
 * from replacing an object already at the key.
 */
export async function uploadSingle(args: {
  parent: string;
  file: File;
  tags: Record<string, string>;
  idempotencyKey: string;
  onProgress: Progress;
}): Promise<string> {
  const { parent, file } = args;
  const checksum = await sha256Base64(file);
  const allocated = await objectClient.uploadObject({
    parent,
    key: file.name,
    contentType: file.type || DEFAULT_CONTENT_TYPE,
    sizeHintBytes: BigInt(file.size),
    checksumAlgorithm: ChecksumAlgorithm.SHA256,
    checksumValue: checksum,
    metadata: {},
    tags: args.tags,
    externalRef: "",
    transport: PresignTransport.PUT,
    idempotencyKey: args.idempotencyKey,
  });
  const url = allocated.uploadUrl;
  if (!url?.url || !allocated.object) {
    throw new Error("UploadObject returned no upload URL");
  }
  const etag = await putWithProgress(
    url.method || "PUT",
    url.url,
    signedRequestHeaders(url.requiredHeaders),
    file,
    args.onProgress,
  );
  await objectClient.completeObject({
    name: allocated.object.name,
    etag: etag ?? "",
    checksumValue: checksum,
  });
  return allocated.object.name;
}

/**
 * Runs Initiate → (hash → PresignPart → PUT) × N → Complete and returns the
 * object's resource name.
 *
 * Each part's URL is signed for that part's exact length and SHA-256, and
 * Complete lists every part again with its checksum. Each part's ETag comes
 * back only from its PUT response, and Complete needs all of them; browsers
 * expose that header only when storage lists it in
 * Access-Control-Expose-Headers, which is why a missing one is reported as
 * configuration rather than as a transfer error.
 *
 * On failure the session is aborted: the parts already stored are released
 * rather than left to bill as storage until the reaper notices.
 */
export async function uploadMultipart(args: {
  parent: string;
  file: File;
  tags: Record<string, string>;
  onProgress: Progress;
}): Promise<string> {
  const { parent, file } = args;
  const init = await multipartClient.initiateMultipartUpload({
    parent,
    key: file.name,
    contentType: file.type || DEFAULT_CONTENT_TYPE,
    sizeBytes: BigInt(file.size),
    tags: args.tags,
    checksumAlgorithm: ChecksumAlgorithm.SHA256,
  });
  const objectName = init.object?.name ?? "";
  if (!objectName || !init.uploadId) {
    throw new Error("InitiateMultipartUpload returned no session");
  }

  // The server picks the part size and count; the part URLs are signed for
  // exactly those lengths, so the slices must match them.
  const partSize = Number(init.recommendedPartSize);
  const partsTotal =
    init.totalParts || Math.max(1, Math.ceil(file.size / partSize));
  const done = new Array<{ etag: string; checksum: string }>(partsTotal);

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
        const checksum = await sha256Base64(slice);

        const signed = await multipartClient.presignPart({
          objectName,
          uploadId: init.uploadId,
          partNumber,
          checksumValue: checksum,
        });
        const url = signed.uploadUrl;
        if (!url?.url) {
          throw new Error(`No presigned URL for part ${partNumber}`);
        }
        const res = await fetch(url.url, {
          method: url.method || "PUT",
          headers: signedRequestHeaders(url.requiredHeaders),
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
        done[index] = { etag, checksum };
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
      parts: done.map((p, i) => ({
        partNumber: i + 1,
        etag: p.etag,
        checksumValue: p.checksum,
      })),
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
