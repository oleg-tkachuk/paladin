import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Verifies the BFF re-issues the call to the backend WITH the browser-minted
// Idempotency-Key (the bug: it forwarded only Authorization, so the backend's
// RequireOnCreate gate rejected every Create*/Issue*). Mocks the internal
// backend's fetch so nothing is actually created.
describe("/api/rpc BFF — Idempotency-Key forwarding", () => {
  let backendHeaders: Headers | null = null;

  beforeEach(() => {
    // planeBackendUrls are read at module load → stub before the dynamic import.
    vi.stubEnv("PALADIN_ADMIN_URL", "https://backend.test");
    vi.stubEnv("PALADIN_DATA_URL", "https://backend.test");
    vi.stubEnv("PALADIN_IAM_URL", "https://backend.test");
    backendHeaders = null;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: unknown, init?: RequestInit) => {
        const r = input as Request;
        const url = typeof input === "string" ? input : r.url;
        const headers = new Headers(
          (init?.headers as HeadersInit | undefined) ?? r.headers,
        );
        if (url.includes("backend.test")) backendHeaders = headers;
        return new Response(new Uint8Array(), { status: 500 });
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  it("forwards the browser Idempotency-Key to the backend on a Create* RPC", async () => {
    const { POST } = await import("./route");
    const req = new Request(
      "https://app.test/api/rpc/admin/paladin.admin.v1.BucketService/CreateBucket",
      {
        method: "POST",
        headers: {
          "content-type": "application/json",
          "connect-protocol-version": "1",
          "idempotency-key": "test-key-123",
        },
        body: JSON.stringify({ parent: "storageBackends/x", bucketName: "b" }),
      },
    );

    await POST(req).catch(() => {});

    expect(backendHeaders, "BFF never called the backend").not.toBeNull();
    // Headers.get is case-insensitive — the browser sends it lowercase.
    expect(backendHeaders?.get("Idempotency-Key")).toBe("test-key-123");
  });
});
