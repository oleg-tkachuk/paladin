import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type React from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  listTenants: vi.fn(),
  updateTenant: vi.fn(),
  deleteTenant: vi.fn(),
  restoreTenant: vi.fn(),
  purgeTenant: vi.fn(),
}));
vi.mock("@/lib/connect/client", () => ({ tenantClient: h }));
vi.mock("@/context/RefreshContext", () => ({
  useBumpRefresh: () => vi.fn(),
}));

import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { tenantsQuery } from "@/lib/queries/tenants";

import { MAX_LIST_PAGES } from "./useBuckets";
import { useTenants } from "./useTenants";

const tenant = (tenantId: string, extra: Partial<Tenant> = {}) =>
  ({ tenantId, displayName: tenantId, labels: {}, ...extra }) as Tenant;

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

function wrap(qc: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

/** A server call the test answers when it chooses. */
function held() {
  let reject!: (e: Error) => void;
  let resolve!: (v: unknown) => void;
  const call = vi.fn(
    () => new Promise((res, rej) => ((resolve = res), (reject = rej))),
  );
  return {
    call,
    refuse: (e: Error) => reject(e),
    answer: (v: unknown) => resolve(v),
  };
}

beforeEach(() => {
  for (const fn of Object.values(h)) fn.mockReset();
});

describe("useTenants reads", () => {
  // The callers pick a tenant from this set (a grant, an owner) and name
  // tenants by it; one page hid every tenant past the first five hundred.
  it("reads every page of the live tenants", async () => {
    h.listTenants
      .mockResolvedValueOnce({
        tenants: [tenant("t-1")],
        page: { nextPageToken: "p2" },
      })
      .mockResolvedValueOnce({ tenants: [tenant("t-2")], page: {} });
    const { result } = renderHook(() => useTenants(), {
      wrapper: wrap(client()),
    });
    await waitFor(() => expect(result.current.tenants).toHaveLength(2));
    expect(h.listTenants.mock.calls[1][0].page.pageToken).toBe("p2");
    expect(h.listTenants.mock.calls[0][0]).toMatchObject({
      includeTrashed: false,
      onlyTrashed: false,
    });
  });

  it("stops at the ceiling rather than paging forever", async () => {
    h.listTenants.mockResolvedValue({
      tenants: [tenant("t")],
      page: { nextPageToken: "again" },
    });
    const { result } = renderHook(() => useTenants(), {
      wrapper: wrap(client()),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(h.listTenants).toHaveBeenCalledTimes(MAX_LIST_PAGES);
  });

  it.each([
    ["trashed", { includeTrashed: false, onlyTrashed: true }],
    ["all", { includeTrashed: true, onlyTrashed: false }],
  ] as const)("reads the %s set with its flags", async (set, flags) => {
    h.listTenants.mockResolvedValue({ tenants: [], page: {} });
    const { result } = renderHook(() => useTenants(set), {
      wrapper: wrap(client()),
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(h.listTenants.mock.calls[0][0]).toMatchObject(flags);
  });

  it("says why the list could not be read", async () => {
    h.listTenants.mockRejectedValue(new Error("unavailable"));
    const { result } = renderHook(() => useTenants(), {
      wrapper: wrap(client()),
    });
    await waitFor(() => expect(result.current.error).toMatch(/unavailable/));
  });
});

describe("useTenants changes", () => {
  /** A client holding live tenants a and b, and a in the trash too. */
  function seeded() {
    const qc = client();
    // The re-read after a change never answers, so what the lists hold
    // afterwards is the prediction or the rollback, not a fresh read.
    h.listTenants.mockImplementation(() => new Promise(() => {}));
    qc.setQueryData(tenantsQuery("active").queryKey, [
      tenant("a"),
      tenant("b"),
    ]);
    qc.setQueryData(tenantsQuery("trashed").queryKey, [tenant("z")]);
    qc.setQueryData(tenantsQuery("all").queryKey, [
      tenant("a"),
      tenant("b"),
      tenant("z"),
    ]);
    const { result } = renderHook(() => useTenants(), { wrapper: wrap(qc) });
    const ids = (set: "active" | "trashed" | "all") =>
      qc
        .getQueryData<Tenant[]>(tenantsQuery(set).queryKey)
        ?.map((t) => t.tenantId);
    const names = (set: "active" | "all") =>
      qc
        .getQueryData<Tenant[]>(tenantsQuery(set).queryKey)
        ?.map((t) => t.displayName);
    return { qc, result, ids, names };
  }

  it("renames at once in every list, and back when refused", async () => {
    const server = held();
    h.updateTenant.mockImplementation(server.call);
    const { result, names } = seeded();
    let done!: Promise<unknown>;
    act(() => {
      done = result.current
        .updateTenantMetadata("a", "v1", "Acme")
        .catch((e) => e);
    });
    await waitFor(() => expect(names("active")).toEqual(["Acme", "b"]));
    expect(names("all")).toEqual(["Acme", "b", "z"]);
    await act(async () => {
      server.refuse(new Error("stale"));
      await done;
    });
    expect(names("active")).toEqual(["a", "b"]);
    expect(names("all")).toEqual(["a", "b", "z"]);
  });

  it("throws the refusal on to the caller", async () => {
    h.updateTenant.mockRejectedValue(new Error("stale"));
    const { result } = seeded();
    await expect(
      act(() => result.current.updateTenantMetadata("a", "v1", "Acme")),
    ).rejects.toThrow("stale");
  });

  it("takes a trashed tenant out of the live list at once", async () => {
    const server = held();
    h.deleteTenant.mockImplementation(server.call);
    const { result, ids } = seeded();
    let done!: Promise<unknown>;
    act(() => {
      done = result.current.deleteTenant("a", "v1").catch((e) => e);
    });
    await waitFor(() => expect(ids("active")).toEqual(["b"]));
    expect(ids("all")).toEqual(["a", "b", "z"]);
    await act(async () => {
      server.refuse(new Error("denied"));
      await done;
    });
    expect(ids("active")).toEqual(["a", "b"]);
  });

  it("takes a restored tenant out of the trash at once", async () => {
    const server = held();
    h.restoreTenant.mockImplementation(server.call);
    const { result, ids } = seeded();
    let done!: Promise<unknown>;
    act(() => {
      done = result.current.restoreTenant("z");
    });
    await waitFor(() => expect(ids("trashed")).toEqual([]));
    await act(async () => {
      server.answer(tenant("z"));
      await done;
    });
  });

  it("takes a purged tenant out of the trash and of every tenant, and back when refused", async () => {
    const server = held();
    h.purgeTenant.mockImplementation(server.call);
    const { result, ids } = seeded();
    let done!: Promise<unknown>;
    act(() => {
      done = result.current.purgeTenant("z").catch((e) => e);
    });
    await waitFor(() => expect(ids("trashed")).toEqual([]));
    expect(ids("all")).toEqual(["a", "b"]);
    await act(async () => {
      server.refuse(new Error("not in trash"));
      await done;
    });
    expect(ids("trashed")).toEqual(["z"]);
    expect(ids("all")).toEqual(["a", "b", "z"]);
  });

  // A trash also lands the tenant in the trash list, which nothing predicts.
  it("reads every tenants list again after a change", async () => {
    h.deleteTenant.mockResolvedValue({});
    const { qc, result } = seeded();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    await act(() => result.current.deleteTenant("a", "v1"));
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["tenants"] });
  });
});
