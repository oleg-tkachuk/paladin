import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  listCollections: vi.fn(),
  deleteCollection: vi.fn(),
  updateCollection: vi.fn(),
  createCollection: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({ collectionClient: h }));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));
vi.mock("@/hooks/useActingTenant", () => ({
  useActingTenantId: () => "t-acting",
}));

import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { collectionsQuery } from "@/lib/queries/collections";

import { useCollections } from "./useCollections";

const row = (tenantId: string, collection: string, extra = {}) =>
  ({ tenantId, collection, displayName: collection, ...extra }) as Collection;

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}
function wrap(qc: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  for (const fn of Object.values(h)) fn.mockReset();
});

describe("useCollections reads", () => {
  it("lists the tenant the page acts on", async () => {
    h.listCollections.mockResolvedValue({ collections: [], page: {} });
    const { result } = renderHook(() => useCollections(), {
      wrapper: wrap(client()),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(h.listCollections.mock.calls[0][0].parent).toBe("tenants/t-acting");
  });

  // The platform's catalogue asks across tenants, and reads every page.
  it("lists every tenant's in catalogue mode, page after page, by the filter", async () => {
    h.listCollections
      .mockResolvedValueOnce({
        collections: [row("t-a", "a")],
        page: { nextPageToken: "t-a:a" },
      })
      .mockResolvedValueOnce({ collections: [row("t-b", "b")], page: {} });
    const { result } = renderHook(
      () => useCollections({ catalogue: true, filter: 'search.contains("x")' }),
      { wrapper: wrap(client()) },
    );
    await waitFor(() => expect(result.current.collections).toHaveLength(2));
    expect(h.listCollections.mock.calls.map((c) => c[0].parent)).toEqual([
      "",
      "",
    ]);
    expect(h.listCollections.mock.calls[1][0].page.pageToken).toBe("t-a:a");
    expect(h.listCollections.mock.calls[0][0].filter).toBe(
      'search.contains("x")',
    );
  });

  it("holds the read until enabled, as a dialog does until it opens", async () => {
    h.listCollections.mockResolvedValue({ collections: [], page: {} });
    const { rerender } = renderHook(
      ({ open }: { open: boolean }) => useCollections({ enabled: open }),
      { wrapper: wrap(client()), initialProps: { open: false } },
    );
    expect(h.listCollections).not.toHaveBeenCalled();
    rerender({ open: true });
    await waitFor(() => expect(h.listCollections).toHaveBeenCalled());
  });

  it("keeps the last result of the scope on screen while a new search loads", async () => {
    h.listCollections.mockResolvedValueOnce({
      collections: [row("t-acting", "a")],
      page: {},
    });
    const { result, rerender } = renderHook(
      ({ filter }: { filter: string }) => useCollections({ filter }),
      { wrapper: wrap(client()), initialProps: { filter: "" } },
    );
    await waitFor(() => expect(result.current.collections).toHaveLength(1));
    h.listCollections.mockImplementation(() => new Promise(() => {}));
    rerender({ filter: 'search.contains("b")' });
    expect(result.current.collections).toHaveLength(1);
  });

  it("says why the list could not be read", async () => {
    h.listCollections.mockRejectedValue(new Error("unavailable"));
    const { result } = renderHook(() => useCollections(), {
      wrapper: wrap(client()),
    });
    await waitFor(() => expect(result.current.error).toMatch(/unavailable/));
  });
});

describe("useCollections changes", () => {
  /** The tenant's list and the catalogue, both holding docs of t-acting. */
  function seeded() {
    const qc = client();
    h.listCollections.mockImplementation(() => new Promise(() => {}));
    qc.setQueryData(collectionsQuery("tenants/t-acting").queryKey, [
      row("t-acting", "docs"),
      row("t-acting", "logs"),
    ]);
    qc.setQueryData(collectionsQuery("").queryKey, [
      row("t-acting", "docs"),
      row("t-b", "docs"),
    ]);
    const { result } = renderHook(() => useCollections(), {
      wrapper: wrap(qc),
    });
    const list = (parent: string) =>
      qc
        .getQueryData<Collection[]>(collectionsQuery(parent).queryKey)
        ?.map((c) => `${c.tenantId}/${c.collection}:${c.displayName}`);
    return { qc, result, list };
  }

  it("takes a deleted collection out of every list at once, only that tenant's", async () => {
    let refuse!: (e: Error) => void;
    h.deleteCollection.mockImplementation(
      () => new Promise((_, rej) => (refuse = rej)),
    );
    const { result, list } = seeded();
    let done!: Promise<unknown>;
    act(() => {
      done = result.current.deleteCollection("docs", "3").catch((e) => e);
    });
    await waitFor(() =>
      expect(list("tenants/t-acting")).toEqual(["t-acting/logs:logs"]),
    );
    expect(list("")).toEqual(["t-b/docs:docs"]);
    await act(async () => {
      refuse(new Error("holds objects"));
      await done;
    });
    expect(list("tenants/t-acting")).toEqual([
      "t-acting/docs:docs",
      "t-acting/logs:logs",
    ]);
    expect(list("")).toEqual(["t-acting/docs:docs", "t-b/docs:docs"]);
  });

  // A row of a list across tenants is deleted in its own tenant.
  it("deletes in the tenant it is given", async () => {
    h.deleteCollection.mockResolvedValue({});
    const { result } = seeded();
    await act(() => result.current.deleteCollection("docs", "3", false, "t-b"));
    expect(h.deleteCollection.mock.calls[0][0].name).toBe(
      "tenants/t-b/collections/docs",
    );
  });

  it("renames at once, only the fields the mask names, and back when refused", async () => {
    let refuse!: (e: Error) => void;
    h.updateCollection.mockImplementation(
      () => new Promise((_, rej) => (refuse = rej)),
    );
    const { qc, result, list } = seeded();
    let done!: Promise<unknown>;
    act(() => {
      done = result.current
        .updateCollection("docs", "3", ["displayName"], {
          displayName: "Documents",
          cedarPolicy: "unsent",
        })
        .catch((e) => e);
    });
    await waitFor(() =>
      expect(list("tenants/t-acting")?.[0]).toBe("t-acting/docs:Documents"),
    );
    expect(list("")).toEqual(["t-acting/docs:Documents", "t-b/docs:docs"]);
    // The policy was not in the mask: it is not predicted to change.
    expect(
      qc.getQueryData<Collection[]>(collectionsQuery("").queryKey)?.[0]
        .cedarPolicy,
    ).toBeUndefined();
    await act(async () => {
      refuse(new Error("stale"));
      await done;
    });
    expect(list("tenants/t-acting")?.[0]).toBe("t-acting/docs:docs");
  });

  it("reads every collections list again after a change", async () => {
    h.createCollection.mockResolvedValue(row("t-acting", "new"));
    const { qc, result } = seeded();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    await act(() => result.current.createCollection("new"));
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["collections"] });
  });
});
