import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@/test/utils";

const h = vi.hoisted(() => ({
  getTenant: vi.fn(),
  roles: ["platform.admin"] as string[],
  tenantId: "t-1",
}));
vi.mock("@/lib/connect/client", () => ({
  tenantClient: { getTenant: h.getTenant },
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({
    status: "authenticated",
    user: { tenantId: h.tenantId, tenantSlug: "acme", roles: h.roles },
  }),
}));

import { DEFAULT_OBJECT_KEY, STORAGE_KEYS } from "@/constants";
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

function CollectionProbe() {
  const { collection, setCollection } = useScope();
  return (
    <button type="button" onClick={() => setCollection("reports")}>
      {collection}
    </button>
  );
}

// The collection belongs to a tenant, so a switch must not carry it across.
describe("ScopeProvider collection", () => {
  it("is kept across renders and reset when the tenant changes", async () => {
    h.getTenant.mockResolvedValue({ displayName: "Acme" });
    const tree = (
      <ScopeProvider>
        <CollectionProbe />
      </ScopeProvider>
    );
    const { rerender } = render(tree);
    act(() => screen.getByRole("button").click());
    expect(screen.getByRole("button")).toHaveTextContent("reports");
    expect(localStorage.getItem(STORAGE_KEYS.scopeCollection)).toBe("reports");

    rerender(tree);
    expect(screen.getByRole("button")).toHaveTextContent("reports");

    h.tenantId = "t-2";
    rerender(
      <ScopeProvider>
        <CollectionProbe />
      </ScopeProvider>,
    );
    await waitFor(() =>
      expect(screen.getByRole("button")).toHaveTextContent(DEFAULT_OBJECT_KEY),
    );
    expect(localStorage.getItem(STORAGE_KEYS.scopeCollection)).toBeNull();
    h.tenantId = "t-1";
  });
});
