import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";

const h = vi.hoisted(() => ({
  listTenants: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({
  tenantClient: { listTenants: h.listTenants },
}));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));

import { MAX_LIST_PAGES } from "./useBuckets";
import { useTenants } from "./useTenants";

beforeEach(() => {
  h.listTenants.mockReset();
});

describe("useTenants.fetchTenants", () => {
  // The callers pick a tenant from this set (a grant, an owner) and name
  // tenants by it; one page hid every tenant past the first five hundred.
  it("reads every page", async () => {
    h.listTenants
      .mockResolvedValueOnce({
        tenants: [{ tenantId: "t-1" }],
        page: { nextPageToken: "p2" },
      })
      .mockResolvedValueOnce({ tenants: [{ tenantId: "t-2" }], page: {} });
    const { result } = renderHook(() => useTenants());
    let got: { tenants: unknown[] } = { tenants: [] };
    await act(async () => {
      got = await result.current.fetchTenants();
    });
    expect(got.tenants).toHaveLength(2);
    expect(result.current.tenants).toHaveLength(2);
    expect(h.listTenants.mock.calls[1][0].page.pageToken).toBe("p2");
  });

  it("stops at the ceiling rather than paging forever", async () => {
    h.listTenants.mockResolvedValue({
      tenants: [{ tenantId: "t" }],
      page: { nextPageToken: "again" },
    });
    const { result } = renderHook(() => useTenants());
    await act(async () => {
      await result.current.fetchTenants();
    });
    expect(h.listTenants).toHaveBeenCalledTimes(MAX_LIST_PAGES);
  });
});
