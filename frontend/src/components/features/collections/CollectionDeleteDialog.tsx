"use client";

import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { ConfirmModal } from "@/components/ui/ConfirmModal";

/**
 * Confirms deleting a Collection. Shared by the platform and tenant collection
 * tables, which each carried a copy; the page owns the delete call.
 */
export function CollectionDeleteDialog({
  target,
  onClose,
  onConfirm,
}: {
  /** The collection to delete; the dialog is open while it is set. */
  target: Collection | null;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}) {
  // DeleteCollection refuses while the collection holds any object, trashed
  // ones included: objects.collection_id is ON DELETE RESTRICT
  // (docs/deletion-semantics.md). "Empty of live objects" undersold that.
  return (
    <ConfirmModal
      isOpen={!!target}
      onClose={onClose}
      onConfirm={onConfirm}
      type="danger"
      title="Delete this Collection?"
      message={
        <p>
          Removes{" "}
          <span className="font-mono text-foreground">
            {target?.collection}
          </span>{" "}
          and its policy. It is refused while the Collection holds any object,
          those in its trash included.
        </p>
      }
      warning="Collections have no trash: the delete is immediate and cannot be undone."
      confirmText="Delete Collection"
      // No trash, and its policy goes with it: the name is typed, so the
      // Collection deleted is the one meant and not its neighbour's row.
      typeToConfirm={target?.collection}
    />
  );
}
