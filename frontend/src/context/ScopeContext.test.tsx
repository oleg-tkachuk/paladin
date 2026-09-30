import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({
  getTenant: vi.fn(),
  roles: ["platform.admin"] as string[],
}));
vi.mock("@/lib/connect/client", () => ({
  tenantClient: { getTenant: h.getTenant },
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({
    status: "authenticated",
    user: { tenantId: "t-1", tenantSlug: "acme", roles: h.roles },
  }),
}));

import { ScopeProvider, useScope } from "./ScopeContext";

function Probe() {
  const { tenant } = useScope();
  return <span data-testid="tenant">{tenant?.displayName ?? "none"}</span>;
}

describe("ScopeProvider tenant record", () => {
  beforeEach(() => {
    h.getTenant.mockReset();
    h.getTenant.mockResolvedValue({ displayName: "Acme" });
  });

  it("reads the tenant for an admin-audience role", async () => {
    render(
      <ScopeProvider>
        <Probe />
      </ScopeProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("tenant")).toHaveTextContent("Acme"),
    );
  });

  // GetTenant is admin-plane; for a tenant.user it could only be refused.
  it("does not ask for it without the admin audience", async () => {
    h.roles = ["tenant.user"];
    render(
      <ScopeProvider>
        <Probe />
      </ScopeProvider>,
    );
    await new Promise((r) => setTimeout(r, 0));
    expect(h.getTenant).not.toHaveBeenCalled();
    h.roles = ["platform.admin"];
  });
});
