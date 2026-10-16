import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

const h = vi.hoisted(() => ({
  pathname: "/",
  tenant: null as null | {
    tenantId: string;
    slug: string;
    displayName: string;
  },
}));
vi.mock("next/navigation", () => ({ usePathname: () => h.pathname }));
vi.mock("@/app/tenants/[id]/tenant-context", () => ({
  useTenantOptional: () => h.tenant,
}));

import { ScopeBadge } from "./ScopeBadge";

describe("ScopeBadge", () => {
  beforeEach(() => {
    h.tenant = null;
  });

  it("says Platform on a platform page", () => {
    h.pathname = "/storage-backends/primary/buckets/b1";
    render(<ScopeBadge />);
    expect(screen.getByText("Platform")).toBeTruthy();
  });

  it("names the tenant a workspace page acts on", () => {
    h.pathname = "/tenants/acme/collections";
    h.tenant = { tenantId: "t-1", slug: "acme", displayName: "Acme Corp" };
    render(<ScopeBadge />);
    expect(screen.getByText("Acme Corp")).toBeTruthy();
    expect(screen.queryByText("Platform")).toBeNull();
  });

  // Before the workspace resolves its tenant, the route still says which.
  it("falls back to the tenant the route names", () => {
    h.pathname = "/tenants/acme";
    render(<ScopeBadge />);
    expect(screen.getByText("acme")).toBeTruthy();
  });
});
