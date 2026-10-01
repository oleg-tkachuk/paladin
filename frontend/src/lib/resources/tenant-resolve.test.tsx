import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";

// The hook canonicalises a UUID URL to the tenant's slug with router.replace.
// A different value in the [id] segment is a different route subtree to the
// app router, so the replace remounts the layout and every page under it. The
// hook used to resolve, render the page, and only then replace — so an
// operator's open dialog closed and a policy being typed was cleared when the
// replace landed. The e2e suite caught it against a cluster, mid-test.
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

  it("holds the page back until a UUID address is replaced by the slug", async () => {
    // The hook rewrites the [id] segment of the address bar, so it needs one.
    window.history.pushState(
      {},
      "",
      `/tenants/${TENANT_UUID}/buckets/primary/b1/policy`,
    );
    const { result } = renderHook(() => useTenantResolve(TENANT_UUID));

    await waitFor(() => expect(replace).toHaveBeenCalledOnce());
    expect(replace).toHaveBeenCalledWith(
      `/tenants/${TENANT_SLUG}/buckets/primary/b1/policy`,
    );
    await act(async () => {});
    // Still loading: the layout keeps its skeleton, so no page renders that
    // the replace would then remount.
    expect(result.current.loading).toBe(true);
    expect(result.current.tenant).toBeNull();
  });

  it("renders straight away on the canonical address", async () => {
    window.history.pushState(
      {},
      "",
      `/tenants/${TENANT_SLUG}/buckets/primary/b1/policy`,
    );
    const { result } = renderHook(() => useTenantResolve(TENANT_SLUG));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.tenant?.tenantId).toBe(TENANT_UUID);
    expect(replace).not.toHaveBeenCalled();
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
