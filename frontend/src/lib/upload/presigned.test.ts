import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  uploadObject: vi.fn(),
  completeObject: vi.fn(async () => ({})),
  initiateMultipartUpload: vi.fn(),
  presignPart: vi.fn(),
  completeMultipartUpload: vi.fn(async () => ({})),
  abortMultipartUpload: vi.fn(async () => ({})),
  regenerateUploadUrl: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  objectClient: {
    uploadObject: h.uploadObject,
    completeObject: h.completeObject,
  },
  multipartClient: {
    initiateMultipartUpload: h.initiateMultipartUpload,
    presignPart: h.presignPart,
    completeMultipartUpload: h.completeMultipartUpload,
    abortMultipartUpload: h.abortMultipartUpload,
  },
  presignClient: { regenerateUploadUrl: h.regenerateUploadUrl },
}));

import {
  sha256Base64,
  signedRequestHeaders,
  uploadMultipart,
  uploadSingle,
} from "./presigned";
import { TRANSFER_ATTEMPTS, retryTiming } from "./retry";

/** What S3 answers, with 403, for a presigned URL past its expiry. */
const EXPIRED_BODY_FOR_TESTS =
  "<Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>";

/** The base64 SHA-256 of "hello". */
const HELLO_SHA256 = "LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ=";

/** A minimal XMLHttpRequest that records what was sent and succeeds. */
class FakeXHR {
  static last: FakeXHR | undefined;
  /** What the next sends answer, in order; past the end, 200. */
  static answers: { status: number; body: string }[] = [];
  static sent: string[] = [];
  method = "";
  url = "";
  headers: Record<string, string> = {};
  body: unknown;
  status = 200;
  responseText = "";
  upload = { onprogress: null as ((e: ProgressEvent) => void) | null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor() {
    FakeXHR.last = this;
  }
  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(name: string, value: string) {
    this.headers[name] = value;
  }
  getResponseHeader(name: string) {
    return name === "ETag" ? '"etag-1"' : null;
  }
  send(body: unknown) {
    this.body = body;
    FakeXHR.sent.push(this.url);
    const answer = FakeXHR.answers.shift();
    if (answer) {
      this.status = answer.status;
      this.responseText = answer.body;
    }
    queueMicrotask(() => this.onload?.());
  }
}

beforeEach(() => {
  vi.stubGlobal("XMLHttpRequest", FakeXHR);
  FakeXHR.answers = [];
  FakeXHR.sent = [];
  // Retries without the wait between them: the tests count attempts.
  vi.spyOn(retryTiming, "backoffMs").mockReturnValue(0);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

describe("sha256Base64", () => {
  it("is the base64 digest S3 compares a body with", async () => {
    expect(await sha256Base64(new Blob(["hello"]))).toBe(HELLO_SHA256);
  });
});

describe("signedRequestHeaders", () => {
  it("keeps every signed header but the ones the browser owns", () => {
    expect(
      signedRequestHeaders({
        Host: "s3.example.com",
        "content-length": "5",
        "Content-Type": "text/plain",
        "X-Amz-Checksum-Sha256": HELLO_SHA256,
        "If-None-Match": "*",
      }),
    ).toEqual({
      "Content-Type": "text/plain",
      "X-Amz-Checksum-Sha256": HELLO_SHA256,
      "If-None-Match": "*",
    });
  });
});

// The single-shot upload used to PUT with Content-Type alone: it dropped
// the URL's required headers and method, and sent no checksum, so a URL
// bound to anything else failed with a signature mismatch.
describe("uploadSingle", () => {
  it("declares the checksum, sends every signed header and completes with it", async () => {
    h.uploadObject.mockResolvedValue({
      object: { name: "objects/o1" },
      uploadUrl: {
        url: "https://s3.example.com/b/k?sig",
        method: "PUT",
        requiredHeaders: {
          Host: "s3.example.com",
          "Content-Length": "5",
          "Content-Type": "text/plain",
          "X-Amz-Checksum-Sha256": HELLO_SHA256,
          "If-None-Match": "*",
        },
      },
    });
    const file = new File(["hello"], "hello.txt", { type: "text/plain" });

    const name = await uploadSingle({
      parent: "tenants/t/collections/c",
      file,
      tags: {},
      idempotencyKey: "idem-1",
      onProgress: () => {},
    });

    expect(name).toBe("objects/o1");
    expect(h.uploadObject).toHaveBeenCalledWith(
      expect.objectContaining({
        sizeHintBytes: BigInt(5),
        checksumValue: HELLO_SHA256,
      }),
    );
    const xhr = FakeXHR.last!;
    expect(xhr.method).toBe("PUT");
    expect(xhr.headers).toEqual({
      "Content-Type": "text/plain",
      "X-Amz-Checksum-Sha256": HELLO_SHA256,
      "If-None-Match": "*",
    });
    expect(h.completeObject).toHaveBeenCalledWith({
      name: "objects/o1",
      etag: "etag-1",
      checksumValue: HELLO_SHA256,
    });
  });

  it("uploads a typeless file as the content type it declared", async () => {
    h.uploadObject.mockResolvedValue({
      object: { name: "objects/o2" },
      uploadUrl: { url: "https://s3/x", method: "PUT", requiredHeaders: {} },
    });
    await uploadSingle({
      parent: "p",
      file: new File(["x"], "blob"),
      tags: {},
      idempotencyKey: "i",
      onProgress: () => {},
    });
    expect(h.uploadObject).toHaveBeenCalledWith(
      expect.objectContaining({ contentType: "application/octet-stream" }),
    );
  });
});

describe("uploadSingle retries", () => {
  const first = { url: "https://s3/first", method: "PUT", requiredHeaders: {} };
  const fresh = { url: "https://s3/fresh", method: "PUT", requiredHeaders: {} };
  const run = () =>
    uploadSingle({
      parent: "p",
      file: new File(["x"], "x.bin"),
      tags: {},
      idempotencyKey: "i",
      onProgress: () => {},
    });
  beforeEach(() => {
    h.uploadObject.mockResolvedValue({
      object: { name: "objects/r1" },
      uploadUrl: first,
    });
    h.regenerateUploadUrl.mockResolvedValue({ uploadUrl: fresh });
  });

  it("sends again through a regenerated URL when the first expired", async () => {
    FakeXHR.answers = [{ status: 403, body: EXPIRED_BODY_FOR_TESTS }];
    await run();
    expect(h.regenerateUploadUrl).toHaveBeenCalledWith({ name: "objects/r1" });
    expect(FakeXHR.sent).toEqual([first.url, fresh.url]);
    expect(h.completeObject).toHaveBeenCalled();
  });

  // The first PUT stored the bytes and its answer was lost: the retry meets
  // If-None-Match as 412, which means done.
  it("completes when a retry finds the object stored", async () => {
    FakeXHR.answers = [
      { status: 503, body: "" },
      { status: 412, body: "" },
    ];
    await run();
    expect(h.completeObject).toHaveBeenCalledWith(
      expect.objectContaining({ name: "objects/r1", etag: "" }),
    );
  });

  it("does not repeat a request storage refused outright", async () => {
    FakeXHR.answers = [{ status: 400, body: "BadDigest" }];
    await expect(run()).rejects.toThrow(/PUT failed \(400\)/);
    expect(FakeXHR.sent).toHaveLength(1);
    expect(h.completeObject).not.toHaveBeenCalled();
  });

  it("gives up after its attempts", async () => {
    FakeXHR.answers = Array.from({ length: 10 }, () => ({
      status: 503,
      body: "",
    }));
    await expect(run()).rejects.toThrow(/PUT failed \(503\)/);
    expect(FakeXHR.sent).toHaveLength(TRANSFER_ATTEMPTS);
  });
});

describe("uploadMultipart", () => {
  const partSize = 4;
  beforeEach(() => {
    h.initiateMultipartUpload.mockResolvedValue({
      object: { name: "objects/m1" },
      uploadId: "up-1",
      recommendedPartSize: BigInt(partSize),
      totalParts: 2,
    });
    h.presignPart.mockImplementation(
      async ({ partNumber }: { partNumber: number }) => ({
        uploadUrl: {
          url: `https://s3/part${partNumber}`,
          method: "PUT",
          requiredHeaders: {
            "Content-Length": "4",
            "X-Amz-Checksum-Sha256": "sig",
          },
        },
      }),
    );
  });

  // Each part is hashed before it is presigned, the URL's signed headers go
  // on its PUT, and completion lists every part with its checksum.
  it("binds every part to its checksum", async () => {
    const fetchMock = vi.fn(
      async () =>
        new Response(null, { status: 200, headers: { ETag: '"pe"' } }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const file = new File(["hellowo"], "big.bin"); // parts "hell" and "owo"

    await uploadMultipart({
      parent: "p",
      file,
      tags: {},
      onProgress: () => {},
    });

    const sums = await Promise.all([
      sha256Base64(new Blob(["hell"])),
      sha256Base64(new Blob(["owo"])),
    ]);
    expect(h.presignPart).toHaveBeenCalledWith(
      expect.objectContaining({ partNumber: 1, checksumValue: sums[0] }),
    );
    expect(h.presignPart).toHaveBeenCalledWith(
      expect.objectContaining({ partNumber: 2, checksumValue: sums[1] }),
    );
    expect(fetchMock).toHaveBeenCalledWith(
      "https://s3/part1",
      expect.objectContaining({
        method: "PUT",
        headers: { "X-Amz-Checksum-Sha256": "sig" },
      }),
    );
    expect(h.completeMultipartUpload).toHaveBeenCalledWith({
      objectName: "objects/m1",
      uploadId: "up-1",
      parts: [
        { partNumber: 1, etag: "pe", checksumValue: sums[0] },
        { partNumber: 2, etag: "pe", checksumValue: sums[1] },
      ],
    });
  });

  it("aborts the session when a part is refused", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("BadDigest", { status: 400 })),
    );
    await expect(
      uploadMultipart({
        parent: "p",
        file: new File(["hellowo"], "big.bin"),
        tags: {},
        onProgress: () => {},
      }),
    ).rejects.toThrow(/Part 1 failed: 400/);
    expect(h.abortMultipartUpload).toHaveBeenCalledWith({
      objectName: "objects/m1",
      uploadId: "up-1",
    });
    expect(h.completeMultipartUpload).not.toHaveBeenCalled();
  });

  it("retries a failed part through a fresh URL", async () => {
    let failedPart2 = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.endsWith("part2") && !failedPart2) {
          failedPart2 = true;
          return new Response("SlowDown", { status: 503 });
        }
        return new Response(null, { status: 200, headers: { ETag: '"pe"' } });
      }),
    );
    await uploadMultipart({
      parent: "p",
      file: new File(["hellowo"], "big.bin"),
      tags: {},
      onProgress: () => {},
    });
    // Part 2 was presigned twice: once per attempt.
    expect(
      h.presignPart.mock.calls.filter(([r]) => r.partNumber === 2),
    ).toHaveLength(2);
    expect(h.completeMultipartUpload).toHaveBeenCalled();
    expect(h.abortMultipartUpload).not.toHaveBeenCalled();
  });

  it("retries a part whose request never got an answer", async () => {
    let failed = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        if (!failed) {
          failed = true;
          throw new TypeError("Failed to fetch");
        }
        return new Response(null, { status: 200, headers: { ETag: '"pe"' } });
      }),
    );
    await uploadMultipart({
      parent: "p",
      file: new File(["hellowo"], "big.bin"),
      tags: {},
      onProgress: () => {},
    });
    expect(h.completeMultipartUpload).toHaveBeenCalled();
  });
});
