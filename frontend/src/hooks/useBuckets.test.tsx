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
  beforeEach(() => h.listBuckets.mockReset());

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
