import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Integration tests for the BFF RPC router (browser → /api/rpc → backend).
// The internal backend's fetch is mocked, so nothing is created; each test
// inspects how the BFF re-issues the call (headers, target URL, gating).
//
// This is the seam that the backend hurl e2e bypasses — it hits the backend
// directly — which is how the dropped-Idempotency-Key bug shipped.

const ADMIN = "https://admin.test";
const DATA = "https://data.test";
const IAM = "https://iam.test";

type BackendCall = { url: string; headers: Headers };
let calls: BackendCall[] = [];

// Minimal unsigned JWT carrying a given `aud` claim — tokenMatchesPlane only
// base64url-decodes the payload (the backend verifies the signature).
function jwt(aud: string): string {
  const payload = Buffer.from(JSON.stringify({ aud })).toString("base64url");
  return `eyJhbGciOiJIUzI1NiJ9.${payload}.sig`;
}

function rpc(
  path: string,
  headers: Record<string, string>,
  body: unknown = {},
): Request {
  return new Request(`https://app.test${path}`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "connect-protocol-version": "1",
      ...headers,
    },
    body: JSON.stringify(body),
  });
}

const BUCKET_CREATE =
  "/api/rpc/admin/paladin.admin.v1.BucketService/CreateBucket";
const BUCKET_LIST = "/api/rpc/admin/paladin.admin.v1.BucketService/ListBuckets";

describe("BFF /api/rpc router", () => {
  beforeEach(() => {
    // planeBackendUrls are read at module load → stub before importing route.
    vi.stubEnv("PALADIN_ADMIN_URL", ADMIN);
    vi.stubEnv("PALADIN_DATA_URL", DATA);
    vi.stubEnv("PALADIN_IAM_URL", IAM);
    calls = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: unknown, init?: RequestInit) => {
        const r = input as Request;
        const url = typeof input === "string" ? input : r.url;
        const headers = new Headers(
          (init?.headers as HeadersInit | undefined) ?? r.headers,
        );
        if (/admin\.test|data\.test|iam\.test/.test(url)) {
          calls.push({ url, headers });
        }
        return new Response(new Uint8Array(), { status: 500 });
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
    vi.resetModules();
  });

  async function post(req: Request) {
    const { POST } = await import("./route");
    return POST(req).catch((e) => e as Response);
  }

  it("forwards the Idempotency-Key to the backend on Create* RPCs", async () => {
    await post(
      rpc(BUCKET_CREATE, {
        authorization: `Bearer ${jwt("paladin-admin")}`,
        "idempotency-key": "key-123",
      }),
    );
    expect(calls).toHaveLength(1);
    expect(calls[0].headers.get("Idempotency-Key")).toBe("key-123");
  });

  it("forwards the Authorization header to the backend", async () => {
    const token = jwt("paladin-admin");
    await post(rpc(BUCKET_LIST, { authorization: `Bearer ${token}` }));
    expect(calls).toHaveLength(1);
    expect(calls[0].headers.get("Authorization")).toBe(`Bearer ${token}`);
  });

  it("does not invent an Idempotency-Key when the browser didn't send one", async () => {
    await post(
      rpc(BUCKET_LIST, { authorization: `Bearer ${jwt("paladin-admin")}` }),
    );
    expect(calls).toHaveLength(1);
    expect(calls[0].headers.get("Idempotency-Key")).toBeNull();
  });

  it("403s and does NOT forward when the token audience mismatches the plane", async () => {
    const res = await post(
      // a data-plane token aimed at the admin plane
      rpc(BUCKET_CREATE, {
        authorization: `Bearer ${jwt("paladin-data")}`,
        "idempotency-key": "key-123",
      }),
    );
    expect(res.status).toBe(403);
    expect(calls).toHaveLength(0);
  });

  it("403s on a malformed token (fails closed)", async () => {
    const res = await post(
      rpc(BUCKET_LIST, { authorization: "Bearer not-a-jwt" }),
    );
    expect(res.status).toBe(403);
    expect(calls).toHaveLength(0);
  });

  it("forwards anonymously when no token is present (backend decides)", async () => {
    await post(rpc("/api/rpc/iam/paladin.iam.v1.UserService/ListUsers", {}));
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toContain("iam.test");
    expect(calls[0].headers.get("Authorization")).toBeNull();
  });

  it("routes each plane prefix to its own backend URL", async () => {
    await post(
      rpc(BUCKET_LIST, { authorization: `Bearer ${jwt("paladin-admin")}` }),
    );
    await post(
      rpc("/api/rpc/data/paladin.data.v1.ObjectService/ListObjects", {
        authorization: `Bearer ${jwt("paladin-data")}`,
      }),
    );
    expect(calls.map((c) => new URL(c.url).host)).toEqual([
      "admin.test",
      "data.test",
    ]);
  });

  it("404s on an unknown plane prefix", async () => {
    const res = await post(rpc("/api/rpc/bogus/Svc/Method", {}));
    expect(res.status).toBe(404);
    expect(calls).toHaveLength(0);
  });

  it("404s on an unknown method", async () => {
    const res = await post(
      rpc("/api/rpc/admin/paladin.admin.v1.BucketService/Nope", {
        authorization: `Bearer ${jwt("paladin-admin")}`,
      }),
    );
    expect(res.status).toBe(404);
    expect(calls).toHaveLength(0);
  });
});
