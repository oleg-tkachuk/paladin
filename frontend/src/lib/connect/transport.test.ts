import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";

import { SESSION_RPC_PREFIX } from "@/constants";
import { onSessionEnded } from "@/lib/session/ended";

// The real client over the real session transport, against a mocked fetch:
// what reaches the wire, and what a refusal does.
describe("session transport", () => {
  let calls: {
    url: string;
    headers: Headers;
    credentials?: RequestCredentials;
  }[];
  let status: number;

  beforeEach(() => {
    calls = [];
    status = 200;
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: unknown, init?: RequestInit) => {
        const req = input as Request;
        const url = typeof input === "string" ? input : req.url;
        calls.push({
          url,
          headers: new Headers(
            (init?.headers as HeadersInit | undefined) ?? req.headers,
          ),
          credentials: init?.credentials ?? req.credentials,
        });
        if (status === 401) {
          return new Response(
            JSON.stringify({ code: "unauthenticated", message: "no session" }),
            {
              status,
              headers: { "content-type": "application/json" },
            },
          );
        }
        return new Response("{}", {
          status,
          headers: { "content-type": "application/json" },
        });
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("sends every plane's call to the session role, by procedure, with no token", async () => {
    const { authClient, bucketClient, objectClient } =
      await import("@/lib/connect/client");
    await authClient.whoAmI({});
    await bucketClient.listBuckets({}).catch(() => {});
    await objectClient
      .listObjects({ parent: "tenants/t/collections/c" })
      .catch(() => {});
    const paths = calls.map(
      (c) => new URL(c.url, "http://console.test").pathname,
    );
    expect(paths).toEqual([
      `${SESSION_RPC_PREFIX}/paladin.iam.v1.AuthService/WhoAmI`,
      `${SESSION_RPC_PREFIX}/paladin.admin.v1.BucketService/ListBuckets`,
      `${SESSION_RPC_PREFIX}/paladin.data.v1.ObjectService/ListObjects`,
    ]);
    for (const c of calls) {
      expect(c.headers.get("Authorization")).toBeNull();
    }
  });

  it("tells the app the session ended when the role answers Unauthenticated", async () => {
    status = 401;
    const ended = vi.fn();
    const off = onSessionEnded(ended);
    const { authClient } = await import("@/lib/connect/client");
    const err = await authClient.whoAmI({}).catch((e: unknown) => e);
    off();
    expect(err).toBeInstanceOf(ConnectError);
    expect((err as ConnectError).code).toBe(Code.Unauthenticated);
    expect(ended).toHaveBeenCalledTimes(1);
  });

  it("does not end the session for any other refusal", async () => {
    status = 403;
    const ended = vi.fn();
    const off = onSessionEnded(ended);
    const { authClient } = await import("@/lib/connect/client");
    await authClient.whoAmI({}).catch(() => {});
    off();
    expect(ended).not.toHaveBeenCalled();
  });
});
