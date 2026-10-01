import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";

// The hook canonicalises a UUID URL to the tenant's slug with router.replace.
// The layout then hands it the slug as a new id — for the same tenant. It used
// to treat that as a different tenant: reset to loading, render the skeleton,
// and unmount every page under the layout. Whatever an operator had typed into
// a page that was opened by UUID was gone, and an open dialog closed.
//
// Named .test.tsx so vitest runs it in jsdom; renderHook needs a DOM.

const replace = vi.fn();
// One object, as Next's router is: the hook's effect depends on it, and a
// fresh object per render would re-run the effect forever.
const router = { replace };
vi.mock("next/navigation", () => ({
  useRouter: () => router,
}));

const getTenant = vi.fn();
vi.mock("@/lib/connect/client", () => ({
  tenantClient: { getTenant: (...args: unknown[]) => getTenant(...args) },
}));

import { useTenantResolve } from "./tenant-resolve";

const TENANT_UUID = "01a0f51c-2918-7c6b-8eac-41bc39e65888";
const TENANT_SLUG = "platform";

function resolvesTo(uuid: string, slug: string) {
  getTenant.mockResolvedValue({
    name: `tenants/${uuid}`,
    slug,
    displayName: "Platform",
    storageLayout: "shared",
  });
}

describe("useTenantResolve", () => {
  beforeEach(() => {
    getTenant.mockReset();
    replace.mockReset();
    resolvesTo(TENANT_UUID, TENANT_SLUG);
  });

  it("keeps the resolved tenant when the URL is canonicalised to its slug", async () => {
    // The hook rewrites the [id] segment of the address bar, so it needs one.
    window.history.pushState(
      {},
      "",
      `/tenants/${TENANT_UUID}/buckets/primary/b1/policy`,
    );
    const { result, rerender } = renderHook(({ id }) => useTenantResolve(id), {
      initialProps: { id: TENANT_UUID },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(replace).toHaveBeenCalledOnce();

    const loadingSeen: boolean[] = [];
    rerender({ id: TENANT_SLUG });
    loadingSeen.push(result.current.loading);
    await act(async () => {});
    loadingSeen.push(result.current.loading);

    expect(loadingSeen, "the layout would unmount its pages").not.toContain(
      true,
    );
    expect(result.current.tenant?.slug).toBe(TENANT_SLUG);
    expect(getTenant).toHaveBeenCalledOnce();
  });

  it("resolves a different tenant afresh", async () => {
    const { result, rerender } = renderHook(({ id }) => useTenantResolve(id), {
      initialProps: { id: TENANT_SLUG },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));

    const otherUUID = "01a0f51c-0000-7000-8000-000000000001";
    resolvesTo(otherUUID, "acme");
    rerender({ id: "acme" });
    await waitFor(() => expect(result.current.tenant?.slug).toBe("acme"));
    expect(getTenant).toHaveBeenCalledTimes(2);
  });

  it("refetches on retry even for the same tenant", async () => {
    const { result } = renderHook(() => useTenantResolve(TENANT_SLUG));
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => result.current.retry());
    await waitFor(() => expect(getTenant).toHaveBeenCalledTimes(2));
  });
});
