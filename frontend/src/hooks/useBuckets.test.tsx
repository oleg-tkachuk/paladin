import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";

const h = vi.hoisted(() => ({ listBuckets: vi.fn() }));
vi.mock("@/lib/connect/client", () => ({
  bucketClient: { listBuckets: h.listBuckets },
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));

import { useBuckets } from "./useBuckets";

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
