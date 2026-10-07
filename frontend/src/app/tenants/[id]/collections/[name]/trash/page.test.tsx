import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@/test/utils";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import { TooltipProvider } from "@/components/ui/Tooltip";

const h = vi.hoisted(() => ({ blocked: null as string | null }));

vi.mock("@/hooks/useObjects", () => ({
  useObjects: () => ({
    objects: [
      {
        objectId: "o1",
        key: "old.pdf",
        name: "tenants/t-1/collections/docs/objects/old.pdf",
        collection: "docs",
        sizeBytes: 1n,
        state: ObjectState.DELETED,
        tags: {},
      },
    ],
    loading: false,
    restoreObject: vi.fn(),
    purgeObject: vi.fn(),
    bulkDeleteObjects: vi.fn(),
    bulkRestoreObjects: vi.fn(),
  }),
}));
vi.mock("../collection-context", () => ({
  useCollection: () => ({ collection: { collection: "docs" } }),
}));
vi.mock("../../../tenant-context", () => ({
  useTenantChangesBlocked: () => h.blocked,
}));

import CollectionTrashPage from "./page";

// Restoring or purging a trashed object changes the tenant, which a tenant in
// the trash refuses.
describe("CollectionTrashPage on a trashed tenant", () => {
  it("holds restore and purge and says why", () => {
    h.blocked = "in the trash";
    render(
      <TooltipProvider>
        <CollectionTrashPage />
      </TooltipProvider>,
    );
    const restore = screen.getByRole("button", { name: /Restore/ });
    expect(restore).toBeDisabled();
    expect(restore).toHaveAttribute("title", "in the trash");
    expect(
      screen.getByRole("button", { name: "Purge old.pdf from storage" }),
    ).toBeDisabled();
  });

  it("leaves them open on a live tenant", () => {
    h.blocked = null;
    render(
      <TooltipProvider>
        <CollectionTrashPage />
      </TooltipProvider>,
    );
    expect(screen.getByRole("button", { name: /Restore/ })).toBeEnabled();
  });
});
