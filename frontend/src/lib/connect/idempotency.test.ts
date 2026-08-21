import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

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
