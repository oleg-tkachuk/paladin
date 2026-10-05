import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";
import userEvent from "@testing-library/user-event";

const h = vi.hoisted(() => ({
  getEffectivePolicy: vi.fn(
    async ({ resourceName }: { resourceName: string }) => ({
      mergedCedarPolicy: "",
      layers: [
        {
          source: "built-in",
          cedarPolicy: "builtin",
          frozen: false,
          evaluatedCedarPolicy: "builtin",
        },
        {
          source: resourceName,
          cedarPolicy: "rule",
          frozen: false,
          evaluatedCedarPolicy: "rule",
        },
      ],
    }),
  ),
  listCollections: vi.fn(async () => ({
    collections: [{ collection: "docs" }],
  })),
  tenantId: "t-1",
}));
vi.mock("@/lib/connect/client", () => ({
  policyClient: { getEffectivePolicy: h.getEffectivePolicy },
  collectionClient: { listCollections: h.listCollections },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: h.tenantId, displayName: "Acme" }),
}));

import TenantPoliciesPage from "./page";

// The tab shows the policy the authorizer evaluates for the tenant — read
// from GetEffectivePolicy for the tenant's own name, and for one of its
// collections once one is picked. It was a stub pointing elsewhere.
describe("TenantPoliciesPage", () => {
  it("shows the tenant's layers, then a collection's", async () => {
    render(<TenantPoliciesPage />);
    expect(await screen.findByText("tenants/t-1")).toBeInTheDocument();
    expect(h.getEffectivePolicy).toHaveBeenCalledWith(
      { resourceName: "tenants/t-1" },
      expect.anything(),
    );

    const user = userEvent.setup();
    await user.click(screen.getByLabelText("Scope"));
    await user.click(await screen.findByRole("option", { name: /docs/ }));
    expect(
      await screen.findByText("tenants/t-1/collections/docs"),
    ).toBeInTheDocument();
    expect(h.getEffectivePolicy).toHaveBeenCalledWith(
      { resourceName: "tenants/t-1/collections/docs" },
      expect.anything(),
    );
  });

  it("says when the policy cannot be read", async () => {
    h.getEffectivePolicy.mockRejectedValueOnce(new Error("denied by policy"));
    render(<TenantPoliciesPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /denied by policy/,
    );
  });

  // The route can change tenants under the same page; a collection picked on
  // the last one is not this one's.
  it("drops a pick from the previous tenant", async () => {
    const { rerender } = render(<TenantPoliciesPage />);
    const user = userEvent.setup();
    await user.click(await screen.findByLabelText("Scope"));
    await user.click(await screen.findByRole("option", { name: /docs/ }));
    await screen.findByText("tenants/t-1/collections/docs");

    h.tenantId = "t-2";
    rerender(<TenantPoliciesPage />);
    expect(await screen.findByText("tenants/t-2")).toBeInTheDocument();
    h.tenantId = "t-1";
  });
});
