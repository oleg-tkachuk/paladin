"use client";

import React, { useState } from "react";

import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import {
  FormDialog,
  FormField,
  FormRow,
  FormSection,
} from "@/components/ui/form-dialog";
import { IdentityField } from "@/components/IdentityField";
import { useNotification } from "@/components/ui/Notification";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { errorMessage } from "@/hooks/errorContract";
import {
  NOTICE_SEVERITIES,
  noticeFromLabels,
  noticeUrl,
  readNotice,
  withNoticeLabels,
  type NoticeSeverity,
} from "@/lib/notice";
import { T } from "@/lib/ui/typography";

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

const SEVERITY_LABEL: Record<NoticeSeverity, string> = {
  info: "Info",
  warning: "Warning",
  critical: "Critical",
};

/** The form's fields, seeded from a tenant. */
interface Form {
  displayName: string;
  notice: string;
  severity: NoticeSeverity;
  url: string;
  permanent: boolean;
}

function formFor(tenant: Tenant | null): Form {
  const notice = noticeFromLabels(tenant?.labels);
  return {
    displayName: tenant?.displayName || "",
    notice: notice?.content ?? "",
    severity: notice?.severity ?? "info",
    url: notice?.url ?? "",
    permanent: notice?.permanent ?? false,
  };
}

/** The tenant's labels as the form would leave them. */
function labelsFor(tenant: Tenant, form: Form): Record<string, string> {
  return withNoticeLabels(
    tenant.labels,
    readNotice({
      content: form.notice,
      severity: form.severity,
      url: form.url,
      permanent: form.permanent,
    }),
  );
}

const sameLabels = (a: Record<string, string>, b: Record<string, string>) =>
  Object.keys(a).length === Object.keys(b).length &&
  Object.entries(a).every(([k, v]) => b[k] === v);

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

  const labels = editing ? labelsFor(editing, form) : {};
  const unchanged =
    form.displayName === (editing?.displayName || "") &&
    sameLabels(labels, editing?.labels ?? {});
  // The server stores any text; the console would drop a link it cannot
  // show safely, so it says so before saving rather than losing it quietly.
  const hasNotice = form.notice.trim() !== "";
  const badUrl =
    hasNotice && form.url.trim() !== "" && noticeUrl(form.url) === undefined
      ? "Give an http(s) address or a path in the console, such as /trash."
      : null;

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
      <FormSection title="Notice">
        <FormField
          label="Notice"
          hint="Shown across the tenant's pages and beside it in the list. Empty for none."
        >
          {(control) => (
            <Textarea
              {...control}
              rows={2}
              placeholder="Read-only while it moves to the new backend."
              value={form.notice}
              onChange={(e) => set("notice", e.target.value)}
            />
          )}
        </FormField>
        <FormRow>
          <FormField label="Severity">
            {(control) => (
              <SelectRoot
                value={form.severity}
                onValueChange={(v) => set("severity", v as NoticeSeverity)}
                disabled={!hasNotice}
              >
                <SelectTrigger {...control}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {NOTICE_SEVERITIES.map((s) => (
                    <SelectItem key={s} value={s}>
                      {SEVERITY_LABEL[s]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </SelectRoot>
            )}
          </FormField>
          <FormField label="Link" error={badUrl} hint="Where Read more goes.">
            {(control) => (
              <Input
                {...control}
                placeholder="https://status.example.com"
                value={form.url}
                disabled={!hasNotice}
                onChange={(e) => set("url", e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
        <div className="flex items-center gap-2">
          <Switch
            aria-label="Permanent"
            checked={form.permanent}
            disabled={!hasNotice}
            onCheckedChange={(v) => set("permanent", v)}
          />
          <span className={T.hint}>
            {form.permanent
              ? "Permanent: nobody can dismiss it"
              : "Each reader can dismiss it"}
          </span>
        </div>
      </FormSection>
    </FormDialog>
  );
}
