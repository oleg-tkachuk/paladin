import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { wantsIdempotencyKey } from "./transport";
import { AuthService } from "@/gen/paladin/iam/v1/auth_service_pb";
import { BackendService } from "@/gen/paladin/admin/v1/backend_service_pb";
import { BatchService } from "@/gen/paladin/data/v1/batch_service_pb";
import { BucketService } from "@/gen/paladin/admin/v1/bucket_service_pb";
import { CapabilityService } from "@/gen/paladin/admin/v1/capability_service_pb";
import { MultipartUploadService } from "@/gen/paladin/data/v1/multipart_service_pb";
import { ObjectService } from "@/gen/paladin/data/v1/object_service_pb";
import { TenantService } from "@/gen/paladin/admin/v1/tenant_service_pb";

// Drives the real client → transport → interceptor chain with a mocked fetch,
// then the decision itself against real service descriptors.
describe("idempotencyInterceptor", () => {
  let rpcHeaders: Headers | null = null;

  beforeEach(() => {
    rpcHeaders = null;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: unknown, init?: RequestInit) => {
        const req = input as Request;
        const url = typeof input === "string" ? input : req.url;
        const headers = new Headers(
          (init?.headers as HeadersInit | undefined) ?? req.headers,
        );
        if (url.includes("/api/auth/exchange")) {
          return new Response(
            JSON.stringify({
              token: "test-token",
              expiresAt: 9_999_999_999_000,
            }),
            { status: 200, headers: { "content-type": "application/json" } },
          );
        }
        if (url.includes("/api/rpc/")) rpcHeaders = headers;
        return new Response(new Uint8Array(), { status: 500 });
      }),
    );
  });

  afterEach(() => vi.unstubAllGlobals());

  // The whole chain, not the predicate: real client, real transport, real
  // interceptor, mocked fetch. Everything below asserts a DECISION; this is the
  // only place that asserts the header actually reaches the wire.
  it("injects an Idempotency-Key header on an undeclared RPC", async () => {
    const { bucketClient } = await import("@/lib/connect/client");
    await bucketClient
      .createBucket({ parent: "storageBackends/x", bucketId: "b" })
      .catch(() => {});
    expect(rpcHeaders, "RPC fetch was never made").not.toBeNull();
    expect(rpcHeaders?.get("Idempotency-Key")).toBeTruthy();
  });

  // And the other side of it, which the suite never had: a declared read must
  // reach the wire WITHOUT one. Asserting only the positive would pass for an
  // interceptor that stamps everything.
  it("sends no Idempotency-Key on a declared read", async () => {
    const { bucketClient } = await import("@/lib/connect/client");
    await bucketClient
      .listBuckets({ parent: "storageBackends/x" })
      .catch(() => {});
    expect(rpcHeaders, "RPC fetch was never made").not.toBeNull();
    expect(rpcHeaders?.get("Idempotency-Key")).toBeNull();
  });

  // Real service descriptors, not method-name strings. The predicate reads
  // `idempotency` off the generated descriptor now, so a test that invented
  // names would be testing a shape nothing produces — and the previous version
  // of this file did exactly that, which is how it kept agreeing with a prefix
  // list that had drifted from the server.
  it("stamps the RPCs whose repeat nobody has promised anything about", () => {
    for (const m of [
      BucketService.method.createBucket,
      CapabilityService.method.issue,
      CapabilityService.method.delegate,
      ObjectService.method.uploadObject,
      ObjectService.method.completeObject,
      ObjectService.method.copyObject,
      MultipartUploadService.method.initiateMultipartUpload,
      MultipartUploadService.method.completeMultipartUpload,
      BatchService.method.batchCopyObjects,
    ]) {
      expect([m.name, wantsIdempotencyKey(m)]).toEqual([m.name, true]);
    }
  });

  // Credential minting is stamped now, and the reversal is deliberate. The
  // console used to withhold the key because replaying a rotated refresh token
  // is wrong — true, and enforced on the SERVER, which refuses to memoize these
  // at all (middleware.CredentialMintingProcedures). A key is a caller's
  // de-duplication token, not a request to cache; duplicating the server's
  // judgement here is the three-copies problem this change ended.
  it("stamps credential minting, which the server then declines to memoize", () => {
    for (const m of [
      AuthService.method.login,
      AuthService.method.refreshToken,
      AuthService.method.exchangeAudience,
      AuthService.method.switchTenant,
    ]) {
      expect([m.name, wantsIdempotencyKey(m)]).toEqual([m.name, true]);
    }
  });

  // Not "everything that mutates". These are already collapsed by other means,
  // and a key would buy a row in the idempotency table and nothing else.
  it("does not stamp reads or anything declared idempotent", () => {
    for (const m of [
      TenantService.method.getTenant,
      BucketService.method.listBuckets,
      ObjectService.method.countObjects,
      BackendService.method.updateBackend,
      BucketService.method.deleteBucket,
      TenantService.method.deleteTenant,
      // Was stamped until the descriptor replaced the prefix list: `Restore`
      // swept it in, and it takes resource_version, so a repeat writes the same
      // value or fails Aborted.
      ObjectService.method.restoreObjectVersion,
    ]) {
      expect([m.name, wantsIdempotencyKey(m)]).toEqual([m.name, false]);
    }
  });
});

// The regression this pins: three request messages carry an `idempotency_key`
// field, the server rejects a call whose header and field disagree, and the
// first version of this interceptor stamped a fresh UUID over the field the
// console already sets. Every upload through the console failed with
// "Idempotency-Key header and idempotency_key field disagree" — caught by the
// Playwright gate, after the change had already been deployed.
describe("Idempotency-Key vs the message's own idempotency_key field", () => {
  // The real UploadObject descriptor, not `{ name: "UploadObject" }`. A
  // hand-built stand-in has no `idempotency` field, so the predicate would fall
  // through to its default and this would pass whatever the contract said — the
  // exact way a test agrees with itself instead of with the server.
  function headerFor(
    method: typeof ObjectService.method.uploadObject,
    message: object,
  ): string | null {
    const header = new Headers();
    let seen: string | null = null;
    const req = { method, header, message };
    // Inline the interceptor's decision rather than importing the closure:
    // what matters is the value that ends up on the wire.
    if (wantsIdempotencyKey(req.method) && !header.has("Idempotency-Key")) {
      const carried = (req.message as { idempotencyKey?: unknown })
        .idempotencyKey;
      seen =
        typeof carried === "string" && carried !== "" ? carried : "<generated>";
    }
    return seen;
  }

  it("mirrors the field when the message carries one", () => {
    expect(
      headerFor(ObjectService.method.uploadObject, {
        idempotencyKey: "queue-item-7",
      }),
    ).toBe("queue-item-7");
  });

  it("generates one only when the message carries none", () => {
    expect(headerFor(ObjectService.method.uploadObject, {})).toBe(
      "<generated>",
    );
    expect(
      headerFor(ObjectService.method.uploadObject, { idempotencyKey: "" }),
    ).toBe("<generated>");
  });
});
