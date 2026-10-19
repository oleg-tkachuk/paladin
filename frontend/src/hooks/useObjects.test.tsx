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
