import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";

const h = vi.hoisted(() => ({
  listCollections: vi.fn(),
  deleteCollection: vi.fn((_req: { name: string }) => Promise.resolve({})),
}));
vi.mock("@/lib/connect/client", () => ({
  collectionClient: {
    listCollections: h.listCollections,
    deleteCollection: h.deleteCollection,
  },
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));
vi.mock("@/hooks/useActingTenant", () => ({
  useActingTenantId: () => "t-acting",
}));

import { useCollections } from "./useCollections";

beforeEach(() => {
  h.listCollections.mockReset();
  h.deleteCollection.mockClear();
});

describe("useCollections", () => {
  it("lists the tenant the page acts on", async () => {
    h.listCollections.mockResolvedValue({ collections: [], page: {} });
    const { result } = renderHook(() => useCollections());
    await act(() => result.current.fetchAllCollections());
    expect(h.listCollections.mock.calls[0][0].parent).toBe("tenants/t-acting");
  });

  // The platform's catalogue asks across tenants, and reads every page.
  it("lists every tenant's in catalogue mode, page after page", async () => {
    h.listCollections
      .mockResolvedValueOnce({
        collections: [{ tenantId: "t-a", collection: "a" }],
        page: { nextPageToken: "t-a:a" },
      })
      .mockResolvedValueOnce({
        collections: [{ tenantId: "t-b", collection: "b" }],
        page: { nextPageToken: "" },
      });
    const { result } = renderHook(() => useCollections({ catalogue: true }));
    let got: { collections: unknown[] } = { collections: [] };
    await act(async () => {
      got = await result.current.fetchAllCollections('search.contains("x")');
    });
    expect(h.listCollections.mock.calls.map((c) => c[0].parent)).toEqual([
      "",
      "",
    ]);
    expect(h.listCollections.mock.calls[1][0].page.pageToken).toBe("t-a:a");
    expect(h.listCollections.mock.calls[0][0].filter).toBe(
      'search.contains("x")',
    );
    expect(got.collections).toHaveLength(2);
  });

  // A row of a list across tenants is deleted in its own tenant.
  it("deletes in the tenant it is given", async () => {
    const { result } = renderHook(() => useCollections({ catalogue: true }));
    await act(() => result.current.deleteCollection("docs", "3", false, "t-b"));
    expect(h.deleteCollection.mock.calls[0][0].name).toBe(
      "tenants/t-b/collections/docs",
    );
  });
});
