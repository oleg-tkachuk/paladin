import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { AUDIENCES } from "@/constants";

// The rotation itself is the IAM plane's; what is under test is the
// bookkeeping around it, so the Connect client is a stub.
const refreshCalls: string[] = [];
const rotations: Record<string, string> = {};
const rotationFails = false;
vi.mock("@connectrpc/connect", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@connectrpc/connect")>();
  return {
    ...actual,
    createClient: () => ({
      refreshToken: async ({ refreshToken }: { refreshToken: string }) => {
        refreshCalls.push(refreshToken);
        if (rotationFails) throw new Error("refresh token rejected");
        return {
          tokens: {
            accessToken: `at-${refreshToken}`,
            refreshToken: rotations[refreshToken] ?? `${refreshToken}-next`,
            accessExpiresInSeconds: 300n,
            refreshExpiresInSeconds: 3600n,
          },
        };
      },
    }),
  };
});

import { dedupWithGrace, refreshIamChain } from "./bff";

// Guards the refresh-token rotation dedup that prevents a staggered BFF sibling
// (/api/auth/me racing an iam /api/auth/exchange, same cookie) from replaying a
// just-consumed refresh token — which the backend would read as RFC-6819 reuse
// and revoke the whole family. See ROTATION_RESULT_GRACE_MS in bff.ts.
describe("dedupWithGrace", () => {
  const GRACE = 30_000;
  let map: Map<string, Promise<number>>;

  beforeEach(() => {
    vi.useFakeTimers();
    map = new Map();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("collapses concurrent callers with the same key to one factory call", async () => {
    let calls = 0;
    const factory = () => {
      calls += 1;
      return Promise.resolve(calls);
    };
    const [a, b, c] = await Promise.all([
      dedupWithGrace(map, "k", GRACE, factory),
      dedupWithGrace(map, "k", GRACE, factory),
      dedupWithGrace(map, "k", GRACE, factory),
    ]);
    expect(calls).toBe(1);
    expect([a, b, c]).toEqual([1, 1, 1]);
  });

  it("serves the cached result to a staggered caller within the grace window", async () => {
    let calls = 0;
    const factory = () => Promise.resolve(++calls);
    const first = await dedupWithGrace(map, "k", GRACE, factory);
    // Timer not yet fired → entry retained → sibling joins the cached pair.
    const second = await dedupWithGrace(map, "k", GRACE, factory);
    expect(calls).toBe(1);
    expect(first).toBe(1);
    expect(second).toBe(1);
  });

  it("re-runs the factory once the grace window elapses", async () => {
    let calls = 0;
    const factory = () => Promise.resolve(++calls);
    await dedupWithGrace(map, "k", GRACE, factory);
    await vi.advanceTimersByTimeAsync(GRACE + 1); // evict the cached entry
    const after = await dedupWithGrace(map, "k", GRACE, factory);
    expect(calls).toBe(2);
    expect(after).toBe(2);
  });

  it("does not cache a failed attempt — the next caller retries", async () => {
    let calls = 0;
    const factory = () => {
      calls += 1;
      return calls === 1
        ? Promise.reject(new Error("rotation failed"))
        : Promise.resolve(calls);
    };
    await expect(dedupWithGrace(map, "k", GRACE, factory)).rejects.toThrow(
      "rotation failed",
    );
    // Failure evicted immediately → retry hits the factory again and succeeds.
    const retry = await dedupWithGrace(map, "k", GRACE, factory);
    expect(calls).toBe(2);
    expect(retry).toBe(2);
  });
});

// The successor walk that used to be tested here is gone with the code. An
// ExchangeAudience whose token a sibling rotation just consumed is now handled
// by the server: refresh_tokens.superseded_at lets it tell "rotated a moment
// ago by its own holder" from "revoked for cause", and it honours the former
// inside a short window. No client keeps a rotation index any more, which is
// what let this process stop being single-replica for that reason.
//
// The dedup below stays, and so does its constraint: two concurrent ROTATIONS
// of one token are a genuine second rotation, not a read, and collapsing them
// still happens in this process.
