import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

// A failed list fell through to "No Collections yet", inviting the operator to
// create one that may already exist; the toast that said otherwise faded.
const h = vi.hoisted(() => ({ listCollections: vi.fn() }));

vi.mock("@/lib/connect/client", () => ({
  collectionClient: { listCollections: h.listCollections },
}));
vi.mock("../tenant-context", () => ({
  useTenant: () => ({ tenantId: "t-1", slug: "acme", displayName: "Acme" }),
}));
vi.mock("@/context/AuthContext", () => ({
  useAuth: () => ({ user: { roles: ["platform.admin"], tenantId: "t-1" } }),
}));
vi.mock("@/hooks/useCollections", () => ({
  useCollections: () => ({
    createCollection: vi.fn(),
    deleteCollection: vi.fn(),
  }),
}));
vi.mock("@/hooks/useBackends", () => ({
  useBackends: () => ({
    backends: [],
    error: null,
    loading: false,
    fetchBackends: vi.fn(),
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));

import TenantCollectionsPage from "./page";

describe("TenantCollectionsPage failed list", () => {
  it("says the list could not be loaded, not that there are none", async () => {
    h.listCollections.mockRejectedValue(new Error("unavailable"));
    render(<TenantCollectionsPage />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Collections could not be loaded/,
    );
    expect(screen.queryByText(/No Collections yet/)).toBeNull();
  });
});
