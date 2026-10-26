import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const h = vi.hoisted(() => ({
  listObjects: vi.fn(async () => ({
    objects: [],
    page: { nextPageToken: "" },
  })),
  batchDeleteObjects: vi.fn(async () => ({})),
  batchCopyObjects: vi.fn(async () => ({})),
}));
vi.mock("@/lib/connect/client", () => ({
  objectClient: { listObjects: h.listObjects },
  batchClient: {
    batchDeleteObjects: h.batchDeleteObjects,
    batchCopyObjects: h.batchCopyObjects,
  },
  presignClient: {},
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({ user: { tenantId: "t1" } }),
}));
vi.mock("@/context/RefreshContext", () => ({
  useRefreshSignal: () => 0,
  useBumpRefresh: () => vi.fn(),
}));

import { useObjects } from "./useObjects";
import { ObjectContentCategory } from "@/gen/paladin/data/v1/object_service_pb";
import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import { SELECTOR_MAX_NAMES } from "@/lib/batchSelector";
import { TenantProvider } from "@/app/tenants/[id]/tenant-context";
import type { ResolvedTenant } from "@/lib/resources/tenant-resolve";

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

// The objects page re-registers its command-palette actions whenever refresh
// changes, and registering updates the actions context, which renders the page
// again. refresh depended on the whole query result — a new object on every
// render — so the page rendered without end ("Maximum update depth exceeded").
describe("useObjects", () => {
  it("keeps refresh the same function across renders", async () => {
    const { result, rerender } = renderHook(
      () => useObjects({ collection: "docs" }),
      { wrapper },
    );
    await waitFor(() => expect(h.listObjects).toHaveBeenCalled());
    const first = result.current.refresh;
    rerender();
    rerender();
    expect(result.current.refresh).toBe(first);
  });
});

// Under /tenants/<id>/ the page acts on the routed tenant. Reading the
// signed-in user's tenant instead listed a platform admin's own tenant on
// every other tenant's objects page — an empty list for a full collection.
describe("useObjects tenant", () => {
  it("lists the routed tenant's collection, not the user's", async () => {
    h.listObjects.mockClear();
    const routed = ({ children }: { children: ReactNode }) =>
      wrapper({
        children: (
          <TenantProvider
            value={
              {
                tenantId: "t-other",
                slug: "other",
                displayName: "Other",
              } as ResolvedTenant
            }
          >
            {children}
          </TenantProvider>
        ),
      });
    renderHook(() => useObjects({ collection: "docs" }), { wrapper: routed });
    await waitFor(() => expect(h.listObjects).toHaveBeenCalled());
    expect(h.listObjects).toHaveBeenCalledWith(
      expect.objectContaining({ parent: "tenants/t-other/collections/docs" }),
    );
  });

  it("lists the user's own tenant outside a tenant route", async () => {
    h.listObjects.mockClear();
    renderHook(() => useObjects({ collection: "docs" }), { wrapper });
    await waitFor(() => expect(h.listObjects).toHaveBeenCalled());
    expect(h.listObjects).toHaveBeenCalledWith(
      expect.objectContaining({ parent: "tenants/t1/collections/docs" }),
    );
  });
});

// A selector takes at most SELECTOR_MAX_NAMES names; a selection that grew
// past it through "load more" was refused whole.
describe("useObjects bulk actions", () => {
  const items = (n: number) =>
    Array.from({ length: n }, (_, i) => ({
      name: `tenants/t1/collections/docs/objects/o${i}`,
      collection: "docs",
    }));

  it("splits a selection past the selector's cap", async () => {
    const { result } = renderHook(() => useObjects({ collection: "docs" }), {
      wrapper,
    });
    await result.current.bulkDeleteObjects(items(SELECTOR_MAX_NAMES + 1));
    expect(h.batchDeleteObjects).toHaveBeenCalledTimes(2);
  });

  it("copies into the other collection under the prefix", async () => {
    const { result } = renderHook(() => useObjects({ collection: "docs" }), {
      wrapper,
    });
    await result.current.bulkCopyObjects(items(1), "archive", "2026/");
    expect(h.batchCopyObjects).toHaveBeenCalledWith({
      sourceParent: "tenants/t1/collections/docs",
      selector: { names: [items(1)[0].name], filter: "" },
      destinationCollection: "tenants/t1/collections/archive",
      destinationKeyPrefix: "2026/",
    });
  });
});

// A folder listing asks one level under its prefix and keeps the folders the
// first page answers.
describe("useObjects folders", () => {
  it("asks for the level and keeps the first page's folders", async () => {
    h.listObjects.mockClear();
    h.listObjects.mockResolvedValueOnce({
      objects: [],
      page: { nextPageToken: "" },
      commonPrefixes: ["a/b/"],
      commonPrefixesTruncated: true,
    } as never);
    const { result } = renderHook(
      () => useObjects({ collection: "docs", prefix: "a/", delimiter: "/" }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.folders).toEqual(["a/b/"]));
    expect(result.current.foldersTruncated).toBe(true);
    expect(h.listObjects).toHaveBeenCalledWith(
      expect.objectContaining({ prefix: "a/", delimiter: "/" }),
    );
  });
});

// The facets narrow the listing on the server, which counts them once, with
// the first page; a later page is the same listing and asks for none.
describe("useObjects facets", () => {
  const FACETS = {
    states: [ObjectState.AVAILABLE],
    contentCategories: [ObjectContentCategory.PDF],
  };
  const COUNTS = [
    { facet: "state", value: "OBJECT_STATE_AVAILABLE", count: 4n },
  ];

  it("sends the facets, counts them with the first page only, and keeps the counts", async () => {
    h.listObjects.mockClear();
    h.listObjects
      .mockResolvedValueOnce({
        objects: [],
        page: { nextPageToken: "p2" },
        facetCounts: COUNTS,
        facetCountsExact: false,
      } as never)
      .mockResolvedValueOnce({
        objects: [],
        page: { nextPageToken: "" },
        facetCounts: [],
        facetCountsExact: false,
      } as never);
    const { result } = renderHook(
      () =>
        useObjects({ collection: "docs", facets: FACETS, countFacets: true }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.facetCounts).toEqual(COUNTS));
    expect(result.current.facetCountsExact).toBe(false);
    expect(h.listObjects).toHaveBeenLastCalledWith(
      expect.objectContaining({ facets: FACETS, countFacets: true }),
    );
    result.current.loadMore?.();
    await waitFor(() => expect(h.listObjects).toHaveBeenCalledTimes(2));
    expect(h.listObjects).toHaveBeenLastCalledWith(
      expect.objectContaining({ facets: FACETS, countFacets: false }),
    );
    // The first page's counts stand for the listing.
    expect(result.current.facetCounts).toEqual(COUNTS);
  });

  it("asks for no counts unless told to", async () => {
    h.listObjects.mockClear();
    renderHook(() => useObjects({ collection: "docs" }), { wrapper });
    await waitFor(() => expect(h.listObjects).toHaveBeenCalled());
    expect(h.listObjects).toHaveBeenCalledWith(
      expect.objectContaining({ countFacets: false, facets: undefined }),
    );
  });
});
