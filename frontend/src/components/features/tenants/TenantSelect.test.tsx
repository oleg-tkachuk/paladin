import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

import { TenantSelect, tenantOptionLabel } from "./TenantSelect";

describe("tenantOptionLabel", () => {
  it.each([
    {
      name: "a display name and a different slug read as both",
      t: { tenantId: "t-1", slug: "acme", displayName: "Acme" },
      want: "Acme (acme)",
    },
    {
      name: "a display name equal to the slug reads once",
      t: { tenantId: "t-1", slug: "acme", displayName: "acme" },
      want: "acme",
    },
    {
      name: "no display name reads as the slug",
      t: { tenantId: "t-1", slug: "acme", displayName: "" },
      want: "acme",
    },
    {
      name: "neither reads as the tenant UUID",
      t: { tenantId: "t-1", slug: "", displayName: "" },
      want: "t-1",
    },
  ])("$name", ({ t, want }) => {
    expect(tenantOptionLabel(t)).toBe(want);
  });
});

describe("TenantSelect", () => {
  const TENANTS = [{ tenantId: "t-1", slug: "acme", displayName: "Acme" }];

  it("takes its name from a FormField control's id", () => {
    render(
      <>
        <label htmlFor="owner">Owner tenant</label>
        <TenantSelect
          value=""
          onValueChange={vi.fn()}
          tenants={TENANTS}
          placeholder="Pick owner"
          label={{ id: "owner" }}
        />
      </>,
    );
    expect(
      screen.getByRole("combobox", { name: "Owner tenant" }),
    ).toHaveAttribute("id", "owner");
  });

  it("takes its name from an aria-label where there is no visible label", () => {
    render(
      <TenantSelect
        value=""
        onValueChange={vi.fn()}
        tenants={TENANTS}
        placeholder="Pick a tenant"
        label={{ "aria-label": "Tenant to grant" }}
      />,
    );
    expect(
      screen.getByRole("combobox", { name: "Tenant to grant" }),
    ).toBeInTheDocument();
  });

  it("is disabled with no tenant to offer", () => {
    render(
      <TenantSelect
        value=""
        onValueChange={vi.fn()}
        tenants={[]}
        placeholder="No tenants"
        label={{ "aria-label": "Tenant to grant" }}
      />,
    );
    expect(screen.getByRole("combobox")).toBeDisabled();
  });
});
