"use client";

import React, { useState } from "react";

import { Input } from "@/components/ui/input";
import {
  FormDialog,
  FormField,
  FormSection,
} from "@/components/ui/form-dialog";
import { IdentityField } from "@/components/IdentityField";
import { NoticeFields } from "@/components/features/notice/NoticeFields";
import { useNotification } from "@/components/ui/Notification";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { errorMessage } from "@/hooks/errorContract";
import {
  labelsWithNoticeForm,
  noticeFormFor,
  noticeLinkError,
  sameLabels,
  type NoticeForm,
} from "@/lib/noticeForm";

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

/** The form's fields, seeded from a tenant: its name and its notice. */
interface Form {
  displayName: string;
  notice: NoticeForm;
}

function formFor(tenant: Tenant | null): Form {
  return {
    displayName: tenant?.displayName || "",
    notice: noticeFormFor(tenant?.labels),
  };
}

/**
 * The tenant's edit dialog, extracted from tenants/page.tsx: its display
 * name, and the notice its pages and its list row show (notice.paladin.io/*
 * labels; lib/notice). Every other label is sent back as it was. Owns its
 * form and submit state and seeds them from the tenant the row menu
 * selected. tenants/page.test.tsx guards the menu → edit → save path.
 */
export function TenantEditDialog({
  editing,
  onClose,
  updateTenantMetadata,
}: TenantEditDialogProps) {
  const { showNotification } = useNotification();
  const [form, setForm] = useState<Form>(() => formFor(editing));
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Seed the fields whenever a new tenant opens the dialog — render-phase
  // adjust-on-change keyed on the `editing` prop identity (not a
  // set-state-in-effect).
  const [seededEditing, setSeededEditing] = useState(editing);
  if (editing !== seededEditing) {
    setSeededEditing(editing);
    if (editing) setForm(formFor(editing));
    setSubmitError(null);
  }

  const set = <K extends keyof Form>(key: K, value: Form[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  const labels = editing
    ? labelsWithNoticeForm(editing.labels, form.notice)
    : {};
  const unchanged =
    form.displayName === (editing?.displayName || "") &&
    sameLabels(labels, editing?.labels ?? {});
  const badUrl = noticeLinkError(form.notice);

  const handleUpdate = async () => {
    if (!editing) return;
    setSubmitError(null);
    try {
      setSubmitting(true);
      await updateTenantMetadata(
        editing.tenantId,
        editing.resourceVersion,
        form.displayName,
        labels,
      );
      showNotification({
        type: "success",
        title: "Tenant updated",
        message: form.displayName || editing.tenantId,
      });
      onClose();
    } catch (err) {
      console.error(err);
      setSubmitError(errorMessage(err, "Failed to update the tenant."));
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
      description="Its display name and notice; the slug has its own rename."
      onSubmit={() => void handleUpdate()}
      submitLabel="Save changes"
      submittingLabel="Saving…"
      submitting={submitting}
      blockedReason={
        unchanged ? "No change to save." : badUrl ? "Fix the link." : null
      }
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
              value={form.displayName}
              onChange={(e) => set("displayName", e.target.value)}
            />
          )}
        </FormField>
      </FormSection>
      <NoticeFields
        form={form.notice}
        onChange={(notice) => set("notice", notice)}
        where="Shown across the tenant's pages and beside it in the list."
      />
    </FormDialog>
  );
}
