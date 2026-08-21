"use client";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { useNotification } from "@/components/ui/Notification";
import { T } from "@/lib/ui/typography";
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
    } catch {
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: "The tenant could not be removed.",
      });
    }
  };

  return (
    <AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this tenant?</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div className="space-y-3">
              <p>
                All data scoped to{" "}
                <span className="font-mono text-foreground">
                  {deleteTarget?.slug || deleteTarget?.tenantId}
                </span>{" "}
                will become inaccessible.
              </p>
              <div className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs space-y-1">
                <p className="font-semibold text-destructive">
                  What gets removed (cascade)
                </p>
                <ul className="list-disc pl-5 text-muted-foreground space-y-0.5">
                  <li>Default backend/bucket binding</li>
                  <li>All Collections + their cedar policies</li>
                  <li>Audit log entries (after retention TTL)</li>
                  <li>API tokens, M2M tokens, capabilities</li>
                </ul>
              </div>
              <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs space-y-1">
                <p className="font-semibold">What stays</p>
                <ul className="list-disc pl-5 text-muted-foreground space-y-0.5">
                  <li>
                    Physical S3 objects under{" "}
                    <span className={T.code}>{"<bucket>/<tenant_id>/…"}</span>
                  </li>
                  <li>
                    In-flight presigned URLs (continue working until TTL
                    expires)
                  </li>
                </ul>
              </div>
              <p className="text-xs italic text-muted-foreground">
                Recovery requires direct database intervention.
              </p>
            </div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={handleDelete}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
          >
            Delete tenant
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
