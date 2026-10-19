import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  listBuckets: vi.fn(),
  listAccessibleBuckets: vi.fn(),
  createBucket: vi.fn(),
  deleteBucket: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({ bucketClient: h }));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));

import type { BucketRead } from "@/lib/queries/buckets";

import { MAX_LIST_PAGES, useBuckets } from "./useBuckets";

function page(ids: string[], nextPageToken = "") {
  return {
    buckets: ids.map((bucketId) => ({ bucketId, backendId: "b1" })),
    page: { nextPageToken },
  };
}

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}
function wrap(qc: QueryClient = client()) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}
const ids = (r: { current: ReturnType<typeof useBuckets> }) =>
  r.current.buckets.map((b) => b.bucketId);

beforeEach(() => {
  for (const fn of Object.values(h)) fn.mockReset();
});

describe("useBuckets pagination", () => {
  // Past the first page a bucket was invisible and unselectable everywhere in
  // the console, with nothing on screen saying so.
  it("follows nextPageToken so buckets past the first page are reachable", async () => {
    h.listBuckets
      .mockResolvedValueOnce(page(["a", "b"], "tok-2"))
      .mockResolvedValueOnce(page(["z"]));
    const { result } = renderHook(() => useBuckets(), { wrapper: wrap() });
    await waitFor(() => expect(ids(result)).toEqual(["a", "b", "z"]));
    expect(h.listBuckets.mock.calls[1][0].page.pageToken).toBe("tok-2");
  });

  it("stops at the page ceiling instead of following a token forever", async () => {
    h.listBuckets.mockResolvedValue(page(["x"], "always-more"));
    const { result } = renderHook(() => useBuckets(), { wrapper: wrap() });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(h.listBuckets).toHaveBeenCalledTimes(MAX_LIST_PAGES);
  });

  // The collection dialog read one backend's buckets and then another's at
  // once; the slower answer used to win and leave the wrong list on screen.
  it("shows the read asked for last, whichever answer comes in last", async () => {
    let answerFirst!: (v: unknown) => void;
    h.listBuckets
      .mockImplementationOnce(() => new Promise((r) => (answerFirst = r)))
      .mockResolvedValueOnce(page(["second"]));
    const { result, rerender } = renderHook(
      ({ read }: { read: BucketRead }) => useBuckets(read),
      { wrapper: wrap(), initialProps: { read: { backendId: "one" } } },
    );
    rerender({ read: { backendId: "two" } });
    await waitFor(() => expect(ids(result)).toEqual(["second"]));
    await act(async () => answerFirst(page(["first"])));
    expect(ids(result)).toEqual(["second"]);
  });
});

describe("useBuckets reads", () => {
  it("asks for a backend's buckets, by the filter", async () => {
    h.listBuckets.mockResolvedValue(page([]));
    renderHook(
      () => useBuckets({ backendId: "minio", filter: "search == 1" }),
      {
        wrapper: wrap(),
      },
    );
    await waitFor(() => expect(h.listBuckets).toHaveBeenCalled());
    expect(h.listBuckets.mock.calls[0][0]).toMatchObject({
      parent: "storageBackends/minio",
      filter: "search == 1",
    });
  });

  it.each([
    [
      "no scope lists across tenants",
      undefined,
      { ownerTenantId: "", usedByTenantId: "" },
    ],
    [
      "owner scope narrows by ownership",
      { ownerTenantId: "t-1" },
      { ownerTenantId: "t-1", usedByTenantId: "" },
    ],
    [
      "used-by scope narrows to the buckets the tenant stores in",
      { usedByTenantId: "t-1" },
      { ownerTenantId: "", usedByTenantId: "t-1" },
    ],
  ])("%s", async (_, tenantScope, want) => {
    h.listBuckets.mockResolvedValue(page([]));
    renderHook(() => useBuckets({ tenantScope }), { wrapper: wrap() });
    await waitFor(() => expect(h.listBuckets).toHaveBeenCalled());
    expect(h.listBuckets).toHaveBeenCalledWith(
      expect.objectContaining(want),
      expect.anything(),
    );
  });

  // ADR-0032: owned plus granted, which an owner filter is not.
  it("asks for the tenant's bindable buckets, every page", async () => {
    h.listAccessibleBuckets
      .mockResolvedValueOnce(page(["own"], "next"))
      .mockResolvedValueOnce(page(["granted"]));
    const { result } = renderHook(
      () => useBuckets({ accessibleTo: "tenants/t-1" }),
      { wrapper: wrap() },
    );
    await waitFor(() => expect(ids(result)).toEqual(["own", "granted"]));
    expect(h.listAccessibleBuckets.mock.calls[0][0].tenant).toBe("tenants/t-1");
    expect(h.listBuckets).not.toHaveBeenCalled();
  });

  it("reads nothing while held", () => {
    renderHook(() => useBuckets(null), { wrapper: wrap() });
    expect(h.listBuckets).not.toHaveBeenCalled();
    expect(h.listAccessibleBuckets).not.toHaveBeenCalled();
  });

  it("says why the list could not be read, and when it has answered", async () => {
    h.listBuckets.mockRejectedValue(new Error("unavailable"));
    const { result } = renderHook(() => useBuckets(), { wrapper: wrap() });
    expect(result.current.fetched).toBe(false);
    await waitFor(() => expect(result.current.error).toMatch(/unavailable/));
    expect(result.current.fetched).toBe(true);
  });
});

describe("useBuckets changes", () => {
  // ADR-0032: every bucket has an owner tenant; the server refuses none.
  it("sends the owner tenant it is given", async () => {
    h.createBucket.mockImplementation(({ bucket }) => Promise.resolve(bucket));
    const { result } = renderHook(() => useBuckets(null), { wrapper: wrap() });
    await act(() =>
      result.current.createBucket("b1", "logs", "", "", true, null, "t-1"),
    );
    expect(h.createBucket.mock.calls[0][0].bucket.ownerTenantId).toBe("t-1");
  });

  // A create or a delete settles in the reconciler: nothing is predicted,
  // and every list reads again.
  it.each(["create", "delete"] as const)(
    "reads every buckets list again after a %s",
    async (change) => {
      h.createBucket.mockResolvedValue({});
      h.deleteBucket.mockResolvedValue({});
      const qc = client();
      const invalidate = vi.spyOn(qc, "invalidateQueries");
      const { result } = renderHook(() => useBuckets(null), {
        wrapper: wrap(qc),
      });
      await act(async () => {
        if (change === "create") {
          await result.current.createBucket(
            "b1",
            "logs",
            "",
            "",
            true,
            null,
            "t-1",
          );
        } else {
          await result.current.deleteBucket("b1", "logs", "v1");
        }
      });
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ["buckets"] });
    },
  );
});
