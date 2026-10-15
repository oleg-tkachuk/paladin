import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";

const h = vi.hoisted(() => ({
  listBuckets: vi.fn(),
  listAccessibleBuckets: vi.fn(),
  createBucket: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: {
    listBuckets: h.listBuckets,
    listAccessibleBuckets: h.listAccessibleBuckets,
    createBucket: h.createBucket,
  },
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));

import { MAX_LIST_PAGES, useBuckets } from "./useBuckets";

function page(ids: string[], nextPageToken = "") {
  return {
    buckets: ids.map((bucketId) => ({ bucketId, backendId: "b1" })),
    page: { nextPageToken },
  };
}

describe("useBuckets pagination", () => {
  beforeEach(() => {
    h.listBuckets.mockReset();
  });

  // A bucket past the first page used to be invisible everywhere in the
  // console — the scope picker, the collection dialog's selector, the tables —
  // because every caller read one page and nothing said the list was cut.
  it("follows nextPageToken so buckets past the first page are reachable", async () => {
    h.listBuckets
      .mockResolvedValueOnce(page(["a", "b"], "tok-2"))
      .mockResolvedValueOnce(page(["y", "z"]));

    const { result } = renderHook(() => useBuckets());
    await act(async () => {
      await result.current.fetchBuckets();
    });

    expect(h.listBuckets).toHaveBeenCalledTimes(2);
    expect(h.listBuckets.mock.calls[1][0].page.pageToken).toBe("tok-2");
    expect(result.current.buckets.map((b) => b.bucketId)).toEqual([
      "a",
      "b",
      "y",
      "z",
    ]);
  });

  // Two overlapping fetches are the norm — the collection dialog opens with no
  // backend chosen and refetches the moment one is defaulted in. Their results
  // are not interchangeable, so the newest request must win regardless of
  // which response arrives last.
  it("ignores a slow earlier fetch that finishes after a newer one", async () => {
    let releaseFirst: (v: unknown) => void = () => {};
    h.listBuckets
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            releaseFirst = () => resolve(page(["stale-a", "stale-b"]));
          }),
      )
      .mockResolvedValueOnce(page(["fresh"]));

    const { result } = renderHook(() => useBuckets());

    let firstDone: Promise<unknown> = Promise.resolve();
    await act(async () => {
      firstDone = result.current.fetchBuckets(); // slow, started first
      await result.current.fetchBuckets("backend-b"); // fast, started second
    });
    expect(result.current.buckets.map((b) => b.bucketId)).toEqual(["fresh"]);

    await act(async () => {
      releaseFirst(null);
      await firstDone;
    });
    expect(result.current.buckets.map((b) => b.bucketId)).toEqual(["fresh"]);
  });

  it("stops at the page ceiling instead of following a token forever", async () => {
    // A token that never clears would otherwise page until the tab dies.
    h.listBuckets.mockResolvedValue(page(["x"], "always-more"));

    const { result } = renderHook(() => useBuckets());
    let out: { nextPageToken: string } | undefined;
    await act(async () => {
      out = await result.current.fetchBuckets();
    });

    expect(h.listBuckets).toHaveBeenCalledTimes(20);
    // The residual token comes back, so a caller can decide to continue.
    expect(out?.nextPageToken).toBe("always-more");
  });
});

// The default-binding picker reads this list. A shared-layout tenant owns no
// bucket, so the owner-filtered list it used to read was always empty.
describe("useBuckets accessible list", () => {
  const tenant = "tenants/acme";

  beforeEach(() => {
    h.listBuckets.mockReset();
    h.listAccessibleBuckets.mockReset();
  });

  it("asks for the tenant's bindable buckets and follows nextPageToken", async () => {
    h.listAccessibleBuckets
      .mockResolvedValueOnce(page(["shared-a", "own-b"], "tok-2"))
      .mockResolvedValueOnce(page(["shared-z"]));

    const { result } = renderHook(() => useBuckets());
    await act(async () => {
      await result.current.fetchAccessibleBuckets(tenant);
    });

    expect(h.listBuckets).not.toHaveBeenCalled();
    expect(h.listAccessibleBuckets).toHaveBeenCalledTimes(2);
    expect(h.listAccessibleBuckets.mock.calls[0][0]).toMatchObject({
      tenant,
      page: { pageToken: "" },
    });
    expect(h.listAccessibleBuckets.mock.calls[1][0]).toMatchObject({
      tenant,
      page: { pageToken: "tok-2" },
    });
    expect(result.current.buckets.map((b) => b.bucketId)).toEqual([
      "shared-a",
      "own-b",
      "shared-z",
    ]);
    expect(result.current.error).toBeNull();
  });

  it("stops at the page ceiling and returns the residual token", async () => {
    h.listAccessibleBuckets.mockResolvedValue(page(["x"], "always-more"));

    const { result } = renderHook(() => useBuckets());
    let out: { nextPageToken: string } | undefined;
    await act(async () => {
      out = await result.current.fetchAccessibleBuckets(tenant);
    });

    expect(h.listAccessibleBuckets).toHaveBeenCalledTimes(MAX_LIST_PAGES);
    expect(out?.nextPageToken).toBe("always-more");
  });

  it("surfaces a failure through error instead of throwing", async () => {
    h.listAccessibleBuckets.mockRejectedValue(new Error("denied by policy"));

    const { result } = renderHook(() => useBuckets());
    let out: { buckets: unknown[] } | undefined;
    await act(async () => {
      out = await result.current.fetchAccessibleBuckets(tenant);
    });

    expect(out?.buckets).toEqual([]);
    expect(result.current.error).toMatch(/denied by policy/);
  });

  // Both fetches write the one `buckets` list, so they share the sequence
  // guard: a slow accessible-list read must not overwrite a newer listing.
  it("loses to a newer fetchBuckets that finishes first", async () => {
    let releaseFirst: (v: unknown) => void = () => {};
    h.listAccessibleBuckets.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          releaseFirst = () => resolve(page(["stale"]));
        }),
    );
    h.listBuckets.mockResolvedValueOnce(page(["fresh"]));

    const { result } = renderHook(() => useBuckets());
    let firstDone: Promise<unknown> = Promise.resolve();
    await act(async () => {
      firstDone = result.current.fetchAccessibleBuckets(tenant);
      await result.current.fetchBuckets();
    });
    await act(async () => {
      releaseFirst(null);
      await firstDone;
    });
    expect(result.current.buckets.map((b) => b.bucketId)).toEqual(["fresh"]);
  });
});

describe("useBuckets tenant scope", () => {
  beforeEach(() => {
    h.listBuckets.mockReset();
    h.listBuckets.mockResolvedValue(page([]));
  });

  it.each([
    {
      name: "no scope lists across tenants",
      scope: undefined,
      want: { ownerTenantId: "", usedByTenantId: "" },
    },
    {
      name: "owner scope narrows by ownership",
      scope: { ownerTenantId: "t-1" },
      want: { ownerTenantId: "t-1", usedByTenantId: "" },
    },
    {
      name: "used-by scope narrows to the buckets the tenant stores in",
      scope: { usedByTenantId: "t-1" },
      want: { ownerTenantId: "", usedByTenantId: "t-1" },
    },
  ])("$name", async ({ scope, want }) => {
    const { result } = renderHook(() => useBuckets());
    await act(async () => {
      await result.current.fetchBuckets(undefined, "", "", scope);
    });
    expect(h.listBuckets).toHaveBeenCalledWith(expect.objectContaining(want));
  });
});

describe("useBuckets createBucket owner", () => {
  beforeEach(() => {
    h.createBucket.mockReset();
    h.createBucket.mockImplementation(({ bucket }) => Promise.resolve(bucket));
  });

  // A dedicated tenant's page created an unowned bucket, which its own list
  // (the buckets the tenant stores in) then did not show.
  it.each([
    { name: "no owner creates a shared bucket", owner: undefined, want: "" },
    { name: "an owner creates the tenant's own", owner: "t-1", want: "t-1" },
  ])("$name", async ({ owner, want }) => {
    const { result } = renderHook(() => useBuckets());
    await act(async () => {
      await result.current.createBucket(
        "b1",
        "logs",
        "",
        "",
        true,
        null,
        owner,
      );
    });
    expect(h.createBucket.mock.calls[0][0].bucket.ownerTenantId).toBe(want);
  });
});
