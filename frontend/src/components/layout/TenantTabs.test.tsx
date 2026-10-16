import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";

const h = vi.hoisted(() => ({ pathname: "/tenants/acme" }));
vi.mock("next/navigation", () => ({ usePathname: () => h.pathname }));

import { TenantTabs } from "./TenantTabs";

const groups = () => screen.getByRole("tablist", { name: "Workspace" });

describe("TenantTabs", () => {
  beforeEach(() => {
    h.pathname = "/tenants/acme";
  });

  it("shows the groups, not every page, in the first row", () => {
    render(<TenantTabs tenantId="acme" />);
    expect(
      within(groups())
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual([
      "Overview",
      "Collections",
      "Storage",
      "Access",
      "Activity",
      "Limits",
    ]);
  });

  it("opens a group on its first page", () => {
    render(<TenantTabs tenantId="acme" />);
    expect(
      within(groups()).getByRole("tab", { name: "Access" }),
    ).toHaveAttribute("href", "/tenants/acme/policies");
  });

  // A page of a group lights the group and itself in the second row.
  it("marks the group and the page a URL is on", () => {
    h.pathname = "/tenants/acme/m2m-tokens";
    render(<TenantTabs tenantId="acme" />);
    expect(
      within(groups()).getByRole("tab", { name: "Access" }),
    ).toHaveAttribute("aria-selected", "true");
    const pages = screen.getByRole("tablist", { name: "Access" });
    expect(
      within(pages).getByRole("tab", { name: "M2M tokens" }),
    ).toHaveAttribute("aria-selected", "true");
    expect(
      within(pages).getByRole("tab", { name: "Policies" }),
    ).toHaveAttribute("aria-selected", "false");
  });

  it("shows no second row for a group of one page", () => {
    h.pathname = "/tenants/acme/collections/docs";
    render(<TenantTabs tenantId="acme" />);
    expect(screen.getAllByRole("tablist")).toHaveLength(1);
  });
});
