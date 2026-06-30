import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { dedupWithGrace } from "./bff";

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
