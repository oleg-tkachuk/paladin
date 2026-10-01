import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AUDIENCES, type Audience } from "@/constants";

// tokenStore keeps its cache, inflight map, channel and fetcher in module
// scope, so every test needs a pristine copy — hence resetModules + dynamic
// import rather than a top-level static import.
type TokenStore = typeof import("./tokenStore");

const SKEW_MS = 30_000;

/** An entry that isFresh() accepts (expiry beyond now + skew). */
function freshEntry(token: string, ttlMs = 10 * 60_000) {
  return { token, expiresAt: Date.now() + ttlMs };
}

/** An entry inside the skew window — technically unexpired, treated as stale. */
function skewedEntry(token: string) {
  return { token, expiresAt: Date.now() + SKEW_MS / 2 };
}

async function loadStore(): Promise<TokenStore> {
  vi.resetModules();
  return import("./tokenStore");
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("getAccessToken", () => {
  let store: TokenStore;

  beforeEach(async () => {
    store = await loadStore();
  });

  it("throws a configuration error when no fetcher was installed", async () => {
    await expect(store.getAccessToken(AUDIENCES.data)).rejects.toThrow(
      /configureTokenStore/,
    );
  });

  it("fetches, caches, and reuses a token without refetching", async () => {
    const fetcher = vi.fn(async () => freshEntry("tok-1"));
    store.configureTokenStore(fetcher);

    await expect(store.getAccessToken(AUDIENCES.data)).resolves.toBe("tok-1");
    await expect(store.getAccessToken(AUDIENCES.data)).resolves.toBe("tok-1");

    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(fetcher).toHaveBeenCalledWith(AUDIENCES.data);
  });

  it("keeps a separate token per audience", async () => {
    const fetcher = vi.fn(async (aud: Audience) => freshEntry(`tok-${aud}`));
    store.configureTokenStore(fetcher);

    const [data, iam, admin] = await Promise.all([
      store.getAccessToken(AUDIENCES.data),
      store.getAccessToken(AUDIENCES.iam),
      store.getAccessToken(AUDIENCES.admin),
    ]);

    expect([data, iam, admin]).toEqual([
      `tok-${AUDIENCES.data}`,
      `tok-${AUDIENCES.iam}`,
      `tok-${AUDIENCES.admin}`,
    ]);
    // Three audiences ⇒ three fetches; they must not share an inflight slot.
    expect(fetcher).toHaveBeenCalledTimes(3);
  });

  // The single-flight property is the whole reason the inflight map exists:
  // N parallel RPCs must cause exactly one refresh, or the backend invalidates
  // jti's faster than the UI can spend them.
  it("collapses parallel calls for one audience into a single fetch", async () => {
    let release!: (e: { token: string; expiresAt: number }) => void;
    const fetcher = vi.fn(
      () =>
        new Promise<{ token: string; expiresAt: number }>((resolve) => {
          release = resolve;
        }),
    );
    store.configureTokenStore(fetcher);

    const calls = [
      store.getAccessToken(AUDIENCES.data),
      store.getAccessToken(AUDIENCES.data),
      store.getAccessToken(AUDIENCES.data),
    ];
    release(freshEntry("shared"));

    await expect(Promise.all(calls)).resolves.toEqual([
      "shared",
      "shared",
      "shared",
    ]);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("refetches after a failure instead of caching the rejection", async () => {
    const fetcher = vi
      .fn<() => Promise<{ token: string; expiresAt: number }>>()
      .mockRejectedValueOnce(new Error("refresh failed"))
      .mockResolvedValueOnce(freshEntry("tok-2"));
    store.configureTokenStore(fetcher);

    await expect(store.getAccessToken(AUDIENCES.data)).rejects.toThrow(
      "refresh failed",
    );
    // The inflight slot must have been released by the .finally handler.
    await expect(store.getAccessToken(AUDIENCES.data)).resolves.toBe("tok-2");
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  // Refreshing exactly at expiry would race the backend, so a token inside the
  // 30s skew window is deliberately treated as already stale.
  it("refetches a token that is inside the expiry skew window", async () => {
    const fetcher = vi
      .fn<() => Promise<{ token: string; expiresAt: number }>>()
      .mockResolvedValue(freshEntry("replacement"));
    store.configureTokenStore(fetcher);
    store.setAccessToken(AUDIENCES.data, skewedEntry("nearly-expired"));

    await expect(store.getAccessToken(AUDIENCES.data)).resolves.toBe(
      "replacement",
    );
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("refetches an already-expired token", async () => {
    const fetcher = vi
      .fn<() => Promise<{ token: string; expiresAt: number }>>()
      .mockResolvedValue(freshEntry("fresh"));
    store.configureTokenStore(fetcher);
    store.setAccessToken(AUDIENCES.data, {
      token: "expired",
      expiresAt: Date.now() - 1,
    });

    await expect(store.getAccessToken(AUDIENCES.data)).resolves.toBe("fresh");
  });
});

describe("setAccessToken / peekAccessToken", () => {
  let store: TokenStore;

  beforeEach(async () => {
    store = await loadStore();
  });

  it("seeds the cache so no fetch is needed", async () => {
    const fetcher = vi.fn(async () => freshEntry("unused"));
    store.configureTokenStore(fetcher);
    store.setAccessToken(AUDIENCES.iam, freshEntry("seeded"));

    await expect(store.getAccessToken(AUDIENCES.iam)).resolves.toBe("seeded");
    expect(fetcher).not.toHaveBeenCalled();
  });

  it("peek returns null for an unknown audience", () => {
    expect(store.peekAccessToken(AUDIENCES.admin)).toBeNull();
  });

  it("peek returns the token without triggering a fetch", () => {
    const fetcher = vi.fn(async () => freshEntry("x"));
    store.configureTokenStore(fetcher);
    store.setAccessToken(AUDIENCES.admin, freshEntry("peeked"));

    expect(store.peekAccessToken(AUDIENCES.admin)).toBe("peeked");
    expect(fetcher).not.toHaveBeenCalled();
  });

  // peek must apply the same freshness rule as getAccessToken, otherwise
  // callers would act on a token the store itself considers stale.
  it("peek treats a skew-window token as absent", () => {
    store.setAccessToken(AUDIENCES.admin, skewedEntry("nearly-expired"));
    expect(store.peekAccessToken(AUDIENCES.admin)).toBeNull();
  });
});

describe("clearAllTokens", () => {
  it("drops every cached audience", async () => {
    const store = await loadStore();
    store.setAccessToken(AUDIENCES.data, freshEntry("a"));
    store.setAccessToken(AUDIENCES.iam, freshEntry("b"));

    store.clearAllTokens();

    expect(store.peekAccessToken(AUDIENCES.data)).toBeNull();
    expect(store.peekAccessToken(AUDIENCES.iam)).toBeNull();
  });

  it("drops the inflight map so a later call refetches", async () => {
    const store = await loadStore();
    const fetcher = vi
      .fn<() => Promise<{ token: string; expiresAt: number }>>()
      .mockResolvedValue(freshEntry("after-logout"));
    store.configureTokenStore(fetcher);

    await store.getAccessToken(AUDIENCES.data);
    store.clearAllTokens();
    await store.getAccessToken(AUDIENCES.data);

    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});

describe("markAudienceStale", () => {
  let store: TokenStore;

  beforeEach(async () => {
    store = await loadStore();
  });

  it("evicts only the named audience", async () => {
    store.setAccessToken(AUDIENCES.data, freshEntry("data-tok"));
    store.setAccessToken(AUDIENCES.iam, freshEntry("iam-tok"));

    store.markAudienceStale(AUDIENCES.data);

    expect(store.peekAccessToken(AUDIENCES.data)).toBeNull();
    expect(store.peekAccessToken(AUDIENCES.iam)).toBe("iam-tok");
  });

  // This is the reason markAudienceStale exists rather than clearAllTokens:
  // parallel 401s must still collapse into ONE refetch. Dropping the inflight
  // map here would fire N /exchange calls that race the BFF's token rotation.
  it("preserves single-flight across parallel 401 self-heals", async () => {
    let release!: (e: { token: string; expiresAt: number }) => void;
    const fetcher = vi.fn(
      () =>
        new Promise<{ token: string; expiresAt: number }>((resolve) => {
          release = resolve;
        }),
    );
    store.configureTokenStore(fetcher);

    const first = store.getAccessToken(AUDIENCES.data);
    // A 401 lands while the refresh is still in flight.
    store.markAudienceStale(AUDIENCES.data);
    const second = store.getAccessToken(AUDIENCES.data);
    release(freshEntry("healed"));

    await expect(Promise.all([first, second])).resolves.toEqual([
      "healed",
      "healed",
    ]);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("is a no-op for an audience that was never cached", () => {
    expect(() => store.markAudienceStale(AUDIENCES.admin)).not.toThrow();
    expect(store.peekAccessToken(AUDIENCES.admin)).toBeNull();
  });
});

describe("cross-tab sync", () => {
  /** Minimal BroadcastChannel double that records posts and can deliver. */
  class FakeChannel {
    static instances: FakeChannel[] = [];
    onmessage: ((e: MessageEvent) => void) | null = null;
    posted: unknown[] = [];
    constructor(public name: string) {
      FakeChannel.instances.push(this);
    }
    postMessage(msg: unknown) {
      this.posted.push(msg);
    }
    close() {}
  }

  beforeEach(() => {
    FakeChannel.instances = [];
    // tokenStore guards on `typeof window` before touching BroadcastChannel,
    // so the node project needs both stubbed to reach the channel path.
    vi.stubGlobal("window", {});
    vi.stubGlobal("BroadcastChannel", FakeChannel);
  });

  it("broadcasts a set when a token is cached", async () => {
    const store = await loadStore();
    const entry = freshEntry("tok");
    store.setAccessToken(AUDIENCES.data, entry);

    expect(FakeChannel.instances).toHaveLength(1);
    expect(FakeChannel.instances[0].name).toBe("paladin-auth");
    expect(FakeChannel.instances[0].posted).toEqual([
      { kind: "set", audience: AUDIENCES.data, entry },
    ]);
  });

  it("broadcasts a clear on logout", async () => {
    const store = await loadStore();
    store.clearAllTokens();

    expect(FakeChannel.instances[0].posted).toEqual([{ kind: "clear" }]);
  });

  it("reuses one channel across many broadcasts", async () => {
    const store = await loadStore();
    store.setAccessToken(AUDIENCES.data, freshEntry("a"));
    store.setAccessToken(AUDIENCES.iam, freshEntry("b"));
    store.clearAllTokens();

    expect(FakeChannel.instances).toHaveLength(1);
    expect(FakeChannel.instances[0].posted).toHaveLength(3);
  });

  // A Login in another tab must populate this tab's cache without it
  // running its own refresh.
  it("applies a set message from another tab", async () => {
    const store = await loadStore();
    store.setAccessToken(AUDIENCES.iam, freshEntry("local")); // opens the channel
    const ch = FakeChannel.instances[0];

    const entry = freshEntry("from-other-tab");
    ch.onmessage?.({
      data: { kind: "set", audience: AUDIENCES.data, entry },
    } as MessageEvent);

    expect(store.peekAccessToken(AUDIENCES.data)).toBe("from-other-tab");
  });

  it("applies a clear message from another tab", async () => {
    const store = await loadStore();
    store.setAccessToken(AUDIENCES.data, freshEntry("local"));
    const ch = FakeChannel.instances[0];

    ch.onmessage?.({ data: { kind: "clear" } } as MessageEvent);

    expect(store.peekAccessToken(AUDIENCES.data)).toBeNull();
  });

  it("a mirrored token satisfies getAccessToken without fetching", async () => {
    const store = await loadStore();
    const fetcher = vi.fn(async () => freshEntry("unused"));
    store.configureTokenStore(fetcher);
    store.setAccessToken(AUDIENCES.iam, freshEntry("local"));
    const ch = FakeChannel.instances[0];

    ch.onmessage?.({
      data: {
        kind: "set",
        audience: AUDIENCES.data,
        entry: freshEntry("mirrored"),
      },
    } as MessageEvent);

    await expect(store.getAccessToken(AUDIENCES.data)).resolves.toBe(
      "mirrored",
    );
    expect(fetcher).not.toHaveBeenCalled();
  });
});

describe("environments without BroadcastChannel", () => {
  // SSR and older browsers: the store must degrade to a plain in-memory cache
  // rather than throwing on import or on every write.
  it("still caches when BroadcastChannel is unavailable", async () => {
    vi.stubGlobal("window", {});
    vi.stubGlobal("BroadcastChannel", undefined);
    const store = await loadStore();

    expect(() =>
      store.setAccessToken(AUDIENCES.data, freshEntry("no-channel")),
    ).not.toThrow();
    expect(store.peekAccessToken(AUDIENCES.data)).toBe("no-channel");
  });

  it("still caches when there is no window at all (SSR)", async () => {
    const store = await loadStore();

    expect(() =>
      store.setAccessToken(AUDIENCES.data, freshEntry("ssr")),
    ).not.toThrow();
    expect(store.peekAccessToken(AUDIENCES.data)).toBe("ssr");
  });
});
