import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@/test/utils";

// A failed list fell through to "No Collections yet", inviting the operator to
// create one that may already exist; the toast that said otherwise faded.
const h = vi.hoisted(() => ({
  listCollections: vi.fn(),
  deleteCollection: vi.fn(),
}));

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
    deleteCollection: h.deleteCollection,
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

// The delete used to look the row up again at confirm, in whatever the list
// held then. A search typed just before confirming changes the query, the list
// is empty until it answers, and the delete went out with no version: the
// server refused it as a 400, which the toast then called "must be empty".
describe("TenantCollectionsPage delete", () => {
  it("sends the version of the row that was chosen, whatever the list holds at confirm", async () => {
    const row = {
      collection: "c1",
      displayName: "",
      bucket: "b1",
      resourceVersion: "7",
    };
    h.listCollections.mockImplementation(({ filter }: { filter: string }) =>
      Promise.resolve({ collections: filter ? [] : [row] }),
    );
    h.deleteCollection.mockResolvedValue(undefined);
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(<TenantCollectionsPage />);

    // The search is typed first and its 300ms debounce fires while the
    // confirm is open, as in the end-to-end run that caught this.
    await screen.findByRole("button", { name: "Actions for c1" });
    await user.type(screen.getByPlaceholderText(/Search by name/), "zzz");
    await user.click(screen.getByRole("button", { name: "Actions for c1" }));
    await user.click(
      await screen.findByRole("menuitem", { name: /Delete Collection/i }),
    );
    await waitFor(() =>
      expect(h.listCollections).toHaveBeenCalledWith(
        expect.objectContaining({ filter: expect.stringContaining("zzz") }),
        expect.anything(),
      ),
    );
    await user.click(
      await screen.findByRole("button", { name: /^Delete Collection$/ }),
    );

    await waitFor(() =>
      expect(h.deleteCollection).toHaveBeenCalledWith("c1", "7"),
    );
  });
});
