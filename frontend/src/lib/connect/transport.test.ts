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
      bucketName: "b",
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
        bucketName: "b",
      }),
    ).rejects.toThrow();
    expect(rpcCalls).toBe(2); // original + exactly one retry, then it stops
  });
});
