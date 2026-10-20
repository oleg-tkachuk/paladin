"use client";

import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { useNotification } from "@/components/ui/Notification";
import { errorMessage } from "@/hooks/errorContract";
import { Tenant } from "@/gen/paladin/admin/v1/types_pb";

interface TenantDeleteDialogProps {
  // The tenant pending deletion, or null when closed. We hold the whole Tenant
  // (not just the id) because a soft delete needs its resource_version for the
  // OCC check — DeleteTenant with force=false rejects an empty resource_version
  // with InvalidArgument ("resource_version is required; pass force=true to
  // bypass"), which previously made every UI delete silently fail.
  deleteTarget: Tenant | null;
  onClose: () => void;
  deleteTenant: (tenantId: string, resourceVersion: string) => Promise<void>;
}

/**
 * Delete-tenant confirmation, extracted from tenants/page.tsx. Owns the delete
 * call; the row menu just sets the target id on the page.
 * tenants/page.test.tsx guards the menu → confirm → delete path.
 */
export function TenantDeleteDialog({
  deleteTarget,
  onClose,
  deleteTenant,
}: TenantDeleteDialogProps) {
  const { showNotification } = useNotification();

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteTenant(deleteTarget.tenantId, deleteTarget.resourceVersion);
      showNotification({
        type: "success",
        title: "Tenant deleted",
        message: deleteTarget.slug || deleteTarget.tenantId,
      });
      onClose();
    } catch (err) {
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: errorMessage(err, "The tenant could not be removed."),
      });
    }
  };

  // DeleteTenant only ever moves a tenant to the trash (see
  // docs/deletion-semantics.md); the copy says so rather than describing a
  // cascade that only PurgeTenant, from the Trash page, performs. For the
  // same reason nothing is typed here: a restore undoes this, and the purge
  // that cannot be undone asks for the slug.
  return (
    <ConfirmModal
      isOpen={!!deleteTarget}
      onClose={onClose}
      onConfirm={handleDelete}
      type="warning"
      title="Delete this tenant?"
      message={
        <p>
          <span className="font-mono text-foreground">
            {deleteTarget?.slug || deleteTarget?.tenantId}
          </span>{" "}
          moves to the trash. Nothing is destroyed: it can be restored from the
          Trash page until it is purged there.
        </p>
      }
      warning="While it is in the trash the tenant is frozen: every change to it is refused, its data stays readable, and its tokens and capabilities can still be revoked."
      confirmText="Delete tenant"
    />
  );
}
