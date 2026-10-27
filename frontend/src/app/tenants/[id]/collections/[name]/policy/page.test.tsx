import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";

import { render, screen, waitFor } from "@/test/utils";

const NAME = "tenants/acme/collections/docs";

const h = vi.hoisted(() => ({
  setCollectionPolicy: vi.fn(),
  setCollection: vi.fn(),
  invalidatePolicyVersions: vi.fn(),
  history: [] as Array<{ resourceName: string; draft?: string }>,
  // One object across renders, as the context's cached one is: a new one
  // each render re-seeds the editor without end.
  collection: {
    name: "tenants/acme/collections/docs",
    resourceVersion: "3",
    cedarPolicy: "permit (principal, action, resource);",
  },
}));
vi.mock("@/lib/connect/client", () => ({
  collectionClient: { setCollectionPolicy: h.setCollectionPolicy },
  policyClient: { validate: vi.fn() },
}));
vi.mock("../../../tenant-context", () => ({
  useTenantChangesBlocked: () => null,
}));
vi.mock("../collection-context", () => ({
  useCollection: () => ({
    collection: h.collection,
    setCollection: h.setCollection,
  }),
}));
vi.mock("@/components/ui/Notification", () => ({
  useNotification: () => ({ showNotification: vi.fn() }),
}));
vi.mock("@/components/features/policy/PolicyHistory", () => ({
  PolicyHistory: (props: { resourceName: string; draft?: string }) => (
    h.history.push(props),
    (<p>history of {props.resourceName}</p>)
  ),
}));
vi.mock("@/lib/queries/policyVersions", () => ({
  invalidatePolicyVersions: h.invalidatePolicyVersions,
}));

import CollectionPolicyPage from "./page";

// The collection's earlier texts sit under its editor, the draft among them,
// and a save reads them again.
describe("CollectionPolicyPage policy history", () => {
  it("shows the collection's history with the draft, and reads it again after a save", async () => {
    h.setCollectionPolicy.mockResolvedValue({
      name: NAME,
      resourceVersion: "4",
    });
    const user = userEvent.setup();
    render(<CollectionPolicyPage />);
    expect(screen.getByText(`history of ${NAME}`)).toBeInTheDocument();
    await user.type(screen.getByRole("textbox"), "x");
    expect(h.history.at(-1)).toEqual({
      resourceName: NAME,
      draft: "permit (principal, action, resource);x",
    });
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(h.invalidatePolicyVersions).toHaveBeenCalledWith(
        expect.anything(),
        NAME,
      ),
    );
  });
});
