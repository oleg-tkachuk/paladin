"use client";

import React, { useState } from "react";

import { Input } from "@/components/ui/input";
import {
  FormDialog,
  FormField,
  FormSection,
} from "@/components/ui/form-dialog";
import { IdentityField } from "@/components/IdentityField";
import { useNotification } from "@/components/ui/Notification";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { errorMessage } from "@/hooks/errorContract";

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
  const [editDisplayName, setEditDisplayName] = useState(
    editing?.displayName || "",
  );
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Seed the field whenever a new tenant opens the dialog — render-phase
  // adjust-on-change keyed on the `editing` prop identity (not a
  // set-state-in-effect).
  const [seededEditing, setSeededEditing] = useState(editing);
  if (editing !== seededEditing) {
    setSeededEditing(editing);
    if (editing) setEditDisplayName(editing.displayName || "");
    setSubmitError(null);
  }

  const unchanged = editDisplayName === (editing?.displayName || "");

  const handleUpdate = async () => {
    if (!editing) return;
    setSubmitError(null);
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
      setSubmitError(errorMessage(err, "Failed to update the display name."));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormDialog
      open={!!editing}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
      title="Edit tenant"
      description="Only the display name changes here; the slug has its own rename."
      onSubmit={() => void handleUpdate()}
      submitLabel="Save changes"
      submittingLabel="Saving…"
      submitting={submitting}
      blockedReason={unchanged ? "No change to save." : null}
      error={submitError}
    >
      <FormSection>
        <div className="space-y-2 rounded-md border bg-muted/30 px-3 py-2">
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
        <FormField label="Display name" hint="Unique across tenants.">
          {(control) => (
            <Input
              {...control}
              autoFocus
              placeholder="Acme Corporation"
              value={editDisplayName}
              onChange={(e) => setEditDisplayName(e.target.value)}
            />
          )}
        </FormField>
      </FormSection>
    </FormDialog>
  );
}
