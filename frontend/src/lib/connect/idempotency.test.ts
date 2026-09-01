import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { wantsIdempotencyKey } from "./transport";

// Drives the real client → transport → interceptor chain with a mocked fetch
// to confirm the idempotencyInterceptor injects an Idempotency-Key on Create*
// RPCs (req.method.name must be the PascalCase proto name for the prefix
// match to fire).
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

  it("injects an Idempotency-Key header on a Create* RPC", async () => {
    const { bucketClient } = await import("@/lib/connect/client");
    await bucketClient
      .createBucket({ parent: "storageBackends/x", bucketId: "b" })
      .catch(() => {});
    expect(rpcHeaders, "RPC fetch was never made").not.toBeNull();
    expect(rpcHeaders?.get("Idempotency-Key")).toBeTruthy();
  });
});

// The classification is a list, and a list drifts. This pins the decisions
// that matter rather than the list itself: what must carry a key, and — more
// importantly — what must not.
describe("which RPCs carry an Idempotency-Key", () => {
  it("stamps the RPCs whose replay is meaningful", () => {
    for (const name of [
      "CreateTenant",
      "CreateBucket",
      "Issue",
      "Delegate",
      "GrantScopes",
      "UploadObject",
      "CompleteObject",
      "InitiateMultipartUpload",
      "CompleteMultipartUpload",
      "CopyObject",
      "RestoreObject",
      "BatchCopyObjects",
    ]) {
      expect(wantsIdempotencyKey(name)).toBe(true);
    }
  });

  // Replaying a credential is worse than not replaying it. Refresh tokens
  // rotate; a memoized response hands back a pair the server already
  // invalidated, and presenting a rotated token is what the theft detector
  // (RFC 6819) revokes an entire family over. The server refuses to memoize
  // these too — this keeps the client from asking.
  it("never stamps a credential-minting RPC", () => {
    for (const name of [
      "Login",
      "RefreshToken",
      "ExchangeAudience",
      "SwitchTenant",
    ]) {
      expect(wantsIdempotencyKey(name)).toBe(false);
    }
  });

  // Not "everything that mutates". These are already collapsed by other
  // means, and a key would buy a row in the idempotency table and nothing else.
  it("does not stamp reads, OCC-guarded writes, or deletes", () => {
    for (const name of [
      "GetTenant",
      "ListBuckets",
      "CountObjects",
      "UpdateBackend",
      "SetQuota",
      "SetBucketPolicy",
      "DeleteBucket",
      "DeleteTenant",
    ]) {
      expect(wantsIdempotencyKey(name)).toBe(false);
    }
  });
});
