"use client";

import React, { useEffect, useState } from "react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { IdentityField } from "@/components/IdentityField";
import { useNotification } from "@/components/ui/Notification";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";

type UpdateTenantFn = (
  tenantId: string,
  resourceVersion: string,
  displayName: string,
  labels: Record<string, string>,
) => Promise<Tenant>;

interface TenantEditDialogProps {
  // The tenant being edited, or null when the dialog is closed.
  editing: Tenant | null;
  onClose: () => void;
  updateTenantMetadata: UpdateTenantFn;
}

/**
 * Edit-display-name dialog, extracted from tenants/page.tsx. Owns its form +
 * submit state and seeds the field from the tenant the row menu selected.
 * tenants/page.test.tsx guards the menu → edit → save path.
 */
export function TenantEditDialog({
  editing,
  onClose,
  updateTenantMetadata,
}: TenantEditDialogProps) {
  const { showNotification } = useNotification();
  const [editDisplayName, setEditDisplayName] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // Seed the field whenever a new tenant opens the dialog (the row menu used
  // to do this inline before the extraction).
  useEffect(() => {
    if (editing) setEditDisplayName(editing.displayName || "");
  }, [editing]);

  const handleUpdate = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!editing) return;
    try {
      setSubmitting(true);
      await updateTenantMetadata(
        editing.tenantId,
        editing.resourceVersion,
        editDisplayName,
        editing.labels,
      );
      showNotification({
        type: "success",
        title: "Tenant updated",
        message: editDisplayName || editing.tenantId,
      });
      onClose();
    } catch (err) {
      console.error(err);
      showNotification({
        type: "error",
        title: "Update failed",
        message: "Failed to update tenant display name.",
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={!!editing} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={handleUpdate}>
          <DialogHeader>
            <DialogTitle>Edit tenant</DialogTitle>
            <DialogDescription>
              Display name is the only editable identity field. Tenant ID and
              slug are immutable — slug rotation requires the RenameTenantSlug
              RPC.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-2 rounded-md border border-border bg-muted/30 px-3 py-2">
              <IdentityField
                label="slug"
                value={editing?.slug || ""}
                immutable
                labelWidth="w-20"
              />
              <IdentityField
                label="tenant id"
                value={editing?.tenantId || ""}
                immutable
                truncate
                labelWidth="w-20"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="edit-display-name">Display name</Label>
              <Input
                id="edit-display-name"
                autoFocus
                placeholder="Acme Corporation"
                value={editDisplayName}
                onChange={(e) => setEditDisplayName(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                Must be unique across tenants.
              </p>
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? "Saving…" : "Save changes"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
