import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@/test/utils";

import { ObjectState } from "@/gen/paladin/data/v1/types_pb";
import { TooltipProvider } from "@/components/ui/Tooltip";

const h = vi.hoisted(() => ({
  blocked: null as string | null,
  restoreObject: vi.fn(),
  purgeObject: vi.fn(),
  bulkDeleteObjects: vi.fn(),
  bulkRestoreObjects: vi.fn(),
}));

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
    restoreObject: h.restoreObject,
    purgeObject: h.purgeObject,
    bulkDeleteObjects: h.bulkDeleteObjects,
    bulkRestoreObjects: h.bulkRestoreObjects,
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

function renderPage() {
  render(
    <TooltipProvider>
      <CollectionTrashPage />
    </TooltipProvider>,
  );
}

// Every restore and purge is confirmed first, with a warning; cancelling
// leaves the object where it was.
describe("CollectionTrashPage confirmations", () => {
  beforeEach(() => {
    h.blocked = null;
    for (const fn of [
      h.restoreObject,
      h.purgeObject,
      h.bulkDeleteObjects,
      h.bulkRestoreObjects,
    ])
      fn.mockReset();
  });

  async function openDialog(button: RegExp | string) {
    fireEvent.click(screen.getByRole("button", { name: button }));
    return screen.findByRole("alertdialog");
  }

  it("restores a row only once confirmed", async () => {
    renderPage();
    const dialog = await openDialog(/^Restore$/);
    expect(dialog).toHaveTextContent("old.pdf");
    expect(within(dialog).getByRole("note")).toHaveTextContent(/refused/);
    expect(h.restoreObject).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument(),
    );
    expect(h.restoreObject).not.toHaveBeenCalled();

    const again = await openDialog(/^Restore$/);
    fireEvent.click(
      within(again).getByRole("button", { name: "Restore object" }),
    );
    await waitFor(() =>
      expect(h.restoreObject).toHaveBeenCalledWith(
        expect.objectContaining({ objectId: "o1" }),
      ),
    );
    expect(h.bulkRestoreObjects).not.toHaveBeenCalled();
  });

  it("purges a row only once confirmed, warning it is final", async () => {
    renderPage();
    const dialog = await openDialog("Purge old.pdf from storage");
    expect(within(dialog).getByRole("note")).toHaveTextContent(
      /cannot be undone/,
    );
    expect(h.purgeObject).not.toHaveBeenCalled();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Purge permanently" }),
    );
    await waitFor(() => expect(h.purgeObject).toHaveBeenCalledOnce());
  });

  it("confirms restoring and purging the selection through the batch calls", async () => {
    renderPage();
    fireEvent.click(screen.getByRole("checkbox", { name: "Select old.pdf" }));

    const bar = () => within(screen.getByRole("toolbar"));
    fireEvent.click(bar().getByRole("button", { name: /^Restore$/ }));
    const restore = await screen.findByRole("alertdialog");
    expect(restore).toHaveTextContent("Restore this object?");
    expect(h.bulkRestoreObjects).not.toHaveBeenCalled();
    fireEvent.click(
      within(restore).getByRole("button", { name: "Restore object" }),
    );
    await waitFor(() =>
      expect(h.bulkRestoreObjects).toHaveBeenCalledWith([
        expect.objectContaining({ collection: "docs" }),
      ]),
    );
    expect(h.restoreObject).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("checkbox", { name: "Select old.pdf" }));
    fireEvent.click(bar().getByRole("button", { name: /^Purge$/ }));
    const purge = await screen.findByRole("alertdialog");
    fireEvent.click(
      within(purge).getByRole("button", { name: "Purge permanently" }),
    );
    await waitFor(() =>
      expect(h.bulkDeleteObjects).toHaveBeenCalledWith(expect.any(Array), {
        permanent: true,
      }),
    );
  });
});
