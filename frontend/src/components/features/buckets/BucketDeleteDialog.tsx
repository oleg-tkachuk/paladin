"use client";

import { useState } from "react";

import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { errorMessage } from "@/hooks/errorContract";
import { useNotification } from "@/components/ui/Notification";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { ConfirmModal } from "@/components/ui/ConfirmModal";

/**
 * Confirms a bucket deletion, optionally of the S3 bucket behind it too. Shared
 * by the platform and tenant bucket tables, which each carried a copy.
 */
export function BucketDeleteDialog({
  target,
  onClose,
  deleteBucket,
}: {
  /** The bucket to delete; the dialog is open while it is set. */
  target: Bucket | null;
  onClose: () => void;
  deleteBucket: (
    backendId: string,
    bucketId: string,
    resourceVersion: string,
    deleteOnBackend: boolean,
  ) => Promise<void>;
}) {
  const { showNotification } = useNotification();
  const [deleteRemote, setDeleteRemote] = useState(false);
  const deleteTarget = target;
  // Only a bucket Paladin created is deleted on the backend (ADR-0028): an
  // adopted one holds data Paladin never wrote.
  const canDeleteRemote = deleteTarget?.createdOnBackend ?? false;
  const close = () => {
    onClose();
    setDeleteRemote(false);
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteBucket(
        deleteTarget.backendId,
        deleteTarget.bucketId,
        deleteTarget.resourceVersion,
        canDeleteRemote && deleteRemote,
      );
      showNotification({
        type: "success",
        title: "Bucket deleted",
        message: `${deleteTarget.bucketId}${canDeleteRemote && deleteRemote ? " (incl. S3)" : ""}`,
      });
      close();
    } catch (err) {
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: errorMessage(err, "Failed to delete bucket."),
      });
    }
  };

  return (
    <ConfirmModal
      isOpen={!!deleteTarget}
      onClose={close}
      onConfirm={handleDelete}
      type="danger"
      title="Delete this bucket?"
      message={
        <p>
          Removes{" "}
          <span className="font-mono text-foreground">
            {deleteTarget?.bucketId}
          </span>{" "}
          from backend{" "}
          <span className="font-mono text-foreground">
            {deleteTarget?.backendId}
          </span>
          . Any Collection still bound to it must be removed first.
        </p>
      }
      warning="Buckets have no trash: the registration goes at once and cannot be restored."
      confirmText="Delete bucket"
      // No trash and no undo, and with the checkbox the S3 bucket goes too,
      // so the operator types the name of the one they mean.
      typeToConfirm={deleteTarget?.bucketId}
    >
      <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-destructive/30 bg-destructive/5 p-3 text-left text-sm has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-60">
        <Checkbox
          checked={canDeleteRemote && deleteRemote}
          disabled={!canDeleteRemote}
          onCheckedChange={(v) => setDeleteRemote(v === true)}
          id="delete-remote"
          className="mt-0.5"
        />
        <div>
          <Label
            htmlFor="delete-remote"
            className="cursor-pointer text-destructive"
          >
            Also delete the physical S3 bucket
          </Label>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {canDeleteRemote
              ? "The S3 bucket must already be empty for this to succeed."
              : "Paladin did not create this bucket, so it does not delete it on the backend; only the registration goes."}
          </p>
        </div>
      </label>
    </ConfirmModal>
  );
}
