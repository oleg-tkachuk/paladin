import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { CollectionDeleteDialog } from "./CollectionDeleteDialog";

const COLLECTION = {
  collection: "invoices",
  resourceVersion: "4",
} as Collection;

function dialog(target: Collection | null = COLLECTION) {
  const onClose = vi.fn();
  const onConfirm = vi.fn().mockResolvedValue(undefined);
  render(
    <CollectionDeleteDialog
      target={target}
      onClose={onClose}
      onConfirm={onConfirm}
    />,
  );
  return { onClose, onConfirm };
}

describe("CollectionDeleteDialog", () => {
  it("is closed without a target", () => {
    dialog(null);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  // A trashed object still blocks the delete (objects.collection_id is
  // RESTRICT); the copy used to speak of live objects only.
  it("names the collection, the trashed-object refusal and the missing trash", () => {
    dialog();
    const confirm = screen.getByRole("alertdialog");
    expect(confirm).toHaveTextContent("invoices");
    expect(confirm).toHaveTextContent(/those in its trash included/);
    expect(screen.getByRole("note")).toHaveTextContent(/no trash/);
  });

  it("deletes only once confirmed", async () => {
    const { onConfirm, onClose } = dialog();
    expect(onConfirm).not.toHaveBeenCalled();
    await userEvent.click(
      screen.getByRole("button", { name: "Delete Collection" }),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("does nothing on cancel", async () => {
    const { onConfirm, onClose } = dialog();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledOnce();
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
