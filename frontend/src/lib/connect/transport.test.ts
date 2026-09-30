import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Exercises the browser transport interceptors end-to-end through a real
// Connect client with a mocked fetch:
//   - auth self-heal: a 401 (Unauthenticated) triggers a token re-exchange
//     via /api/auth/exchange and a single retry of the RPC.
//   - idempotency: Create* RPCs carry an Idempotency-Key (see also
//     idempotency.test.ts which asserts the header value).

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

describe("transport interceptors", () => {
  let rpcCalls = 0;
  let exchangeCalls = 0;

  beforeEach(() => {
    rpcCalls = 0;
    exchangeCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: unknown) => {
        const url = typeof input === "string" ? input : (input as Request).url;
        if (url.includes("/api/auth/exchange")) {
          exchangeCalls += 1;
          return json({
            token: `tok-${exchangeCalls}`,
            expiresAt: 9_999_999_999_000,
          });
        }
        if (url.includes("/api/rpc/")) {
          rpcCalls += 1;
          // First attempt 401s; the auth interceptor should re-exchange and
          // retry, and the second attempt succeeds (empty unary response).
          if (rpcCalls === 1) {
            return json(
              { code: "unauthenticated", message: "token expired" },
              401,
            );
          }
          return json({});
        }
        return new Response(null, { status: 404 });
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it("self-heals a 401 by re-exchanging the token and retrying once", async () => {
    const { bucketClient } = await import("@/lib/connect/client");
    const res = await bucketClient.createBucket({
      parent: "storageBackends/x",
      bucketId: "b",
    });
    expect(res).toBeDefined(); // resolved — the retry succeeded
    expect(rpcCalls).toBe(2); // original + one retry
    expect(exchangeCalls).toBeGreaterThanOrEqual(2); // initial token + re-exchange
  });

  it("surfaces a persistent 401 as a ConnectError (single retry, no loop)", async () => {
    // Every RPC attempt 401s — the interceptor retries exactly once, then
    // gives up rather than looping.
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: unknown) => {
        const url = typeof input === "string" ? input : (input as Request).url;
        if (url.includes("/api/auth/exchange")) {
          exchangeCalls += 1;
          return json({
            token: `tok-${exchangeCalls}`,
            expiresAt: 9_999_999_999_000,
          });
        }
        if (url.includes("/api/rpc/")) {
          rpcCalls += 1;
          return json({ code: "unauthenticated", message: "nope" }, 401);
        }
        return new Response(null, { status: 404 });
      }),
    );
    const { bucketClient } = await import("@/lib/connect/client");
    await expect(
      bucketClient.createBucket({
        parent: "storageBackends/x",
        bucketId: "b",
      }),
    ).rejects.toThrow();
    expect(rpcCalls).toBe(2); // original + exactly one retry, then it stops
  });
});

// When the token cannot be minted at all, the request still goes out
// unauthenticated and the backend answers "missing Authorization header".
// That is the symptom; the cause is the mint. Reporting the symptom is how one
// e2e failure got diagnosed twice — as a console that forgot a header, when
// the session's refresh family had been revoked.
describe("auth interceptor with no mintable token", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it("reports why the token was missing, not that it was missing", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: unknown) => {
        const url = typeof input === "string" ? input : (input as Request).url;
        if (url.includes("/api/auth/exchange")) {
          return json({ error: "refresh token rejected" }, 401);
        }
        return json(
          { code: "unauthenticated", message: "missing Authorization header" },
          401,
        );
      }),
    );

    const { tenantClient } = await import("@/lib/connect/client");
    await expect(tenantClient.listTenants({})).rejects.toThrow(
      /no paladin-admin access token/,
    );
  });
});

// IAM refusing the audience is a fact about the principal, not a lost token.
// Sending the RPC anyway and re-minting on its 401 cost every admin call two
// refused exchanges and a request the backend could only reject.
describe("auth interceptor when the audience is refused", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
  });

  it("fails with PermissionDenied without sending the RPC", async () => {
    let exchangeCalls = 0;
    let rpcCalls = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: unknown) => {
        const url = typeof input === "string" ? input : (input as Request).url;
        if (url.includes("/api/auth/exchange")) {
          exchangeCalls += 1;
          return json(
            { error: "insufficient role for paladin-admin audience" },
            403,
          );
        }
        rpcCalls += 1;
        return json({});
      }),
    );

    const { Code, ConnectError } = await import("@connectrpc/connect");
    const { tenantClient } = await import("@/lib/connect/client");
    const err = await tenantClient.listTenants({}).catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ConnectError);
    expect((err as InstanceType<typeof ConnectError>).code).toBe(
      Code.PermissionDenied,
    );
    expect(rpcCalls).toBe(0);
    expect(exchangeCalls).toBe(1);
  });
});
