"use client";

import { useState } from "react";
import { ConnectError } from "@connectrpc/connect";

import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import {
  FormDialog,
  FormDisclosure,
  FormField,
  FormRow,
  FormSection,
} from "@/components/ui/form-dialog";
import { useNotification } from "@/components/ui/Notification";
import type { CreateBackendInput } from "@/hooks/useBackends";
import {
  StorageKind,
  type StorageBackend,
} from "@/gen/paladin/admin/v1/types_pb";
import {
  REGISTRABLE_STORAGE_KINDS,
  STORAGE_KIND_LABELS,
} from "@/lib/storageKind";

// backend_id format mirrors the `backend_id` CHECK on the buckets table
// and the existing `storage.backends.id` config keys (kebab-case, 3..63
// chars, ASCII alnum + '-'). Letting the server reject is fine but a
// client-side hint avoids the round-trip.
const BACKEND_ID_RE = /^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$/;
export const BACKEND_ID_ERROR =
  "3–63 characters: lowercase letters, digits and hyphens, not at either end.";

const EMPTY_FORM: CreateBackendInput = {
  backendId: "",
  displayName: "",
  kind: StorageKind.S3_COMPATIBLE,
  endpoint: "",
  publicEndpoint: "",
  region: "",
  forcePathStyle: true,
  credentialsSecretRef: "",
};

interface BackendRegisterDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  createBackend: (input: CreateBackendInput) => Promise<StorageBackend>;
}

export function BackendRegisterDialog({
  open,
  onOpenChange,
  createBackend,
}: BackendRegisterDialogProps) {
  const { showNotification } = useNotification();
  const [form, setForm] = useState<CreateBackendInput>(EMPTY_FORM);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const set = <K extends keyof CreateBackendInput>(
    key: K,
    value: CreateBackendInput[K],
  ) => setForm((f) => ({ ...f, [key]: value }));

  const idError =
    form.backendId && !BACKEND_ID_RE.test(form.backendId)
      ? BACKEND_ID_ERROR
      : null;
  const blockedReason = !form.backendId
    ? "Enter a backend ID to continue."
    : idError
      ? "Fix the backend ID to continue."
      : !form.endpoint.trim()
        ? "Enter the internal endpoint."
        : !form.credentialsSecretRef.trim()
          ? "Enter the credentials reference."
          : null;

  const close = (o: boolean) => {
    if (!o) {
      setForm(EMPTY_FORM);
      setSubmitError(null);
    }
    onOpenChange(o);
  };

  const handleCreate = async () => {
    setSubmitError(null);
    try {
      setSubmitting(true);
      const created = await createBackend(form);
      showNotification({
        type: "success",
        title: "Backend registered",
        message: created.displayName || created.backendId,
      });
      close(false);
    } catch (err) {
      setSubmitError(
        err instanceof ConnectError
          ? err.rawMessage
          : err instanceof Error
            ? err.message
            : String(err),
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={close}
      title="Register storage backend"
      description="An S3-compatible target that buckets and tenants bind to."
      width="lg"
      onSubmit={() => void handleCreate()}
      submitLabel="Register backend"
      submittingLabel="Registering…"
      submitting={submitting}
      blockedReason={blockedReason}
      error={submitError}
    >
      <FormSection title="Identity">
        <FormRow>
          <FormField
            label="Backend ID"
            required
            error={idError}
            hint="Used in resource names and bucket refs. Cannot be changed later."
          >
            {(control) => (
              <Input
                {...control}
                autoFocus
                placeholder="aws-eu, r2-global, minio-dev"
                value={form.backendId}
                onChange={(e) => set("backendId", e.target.value)}
              />
            )}
          </FormField>
          <FormField label="Display name">
            {(control) => (
              <Input
                {...control}
                placeholder={form.backendId || "AWS Frankfurt"}
                value={form.displayName}
                onChange={(e) => set("displayName", e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
      </FormSection>

      <FormSection title="Connection">
        <FormRow>
          <FormField label="Kind">
            {(control) => (
              <SelectRoot
                value={String(form.kind)}
                onValueChange={(v) => set("kind", Number(v) as StorageKind)}
              >
                <SelectTrigger {...control}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {REGISTRABLE_STORAGE_KINDS.map((k) => (
                    <SelectItem key={k} value={String(k)}>
                      {STORAGE_KIND_LABELS[k]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </SelectRoot>
            )}
          </FormField>
          <FormField label="Region">
            {(control) => (
              <Input
                {...control}
                placeholder="eu-central-1"
                value={form.region}
                onChange={(e) => set("region", e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
        <FormField
          label="Internal endpoint"
          required
          hint="Where the planes reach the store."
        >
          {(control) => (
            <Input
              {...control}
              placeholder="https://s3.eu-central-1.amazonaws.com"
              className="font-mono text-xs"
              value={form.endpoint}
              onChange={(e) => set("endpoint", e.target.value)}
            />
          )}
        </FormField>
        <FormField
          label="Credentials reference"
          required
          hint="A reference into the secret store. The credentials are never stored or returned."
        >
          {(control) => (
            <Input
              {...control}
              placeholder="vault://kv/paladin/primary"
              className="font-mono text-xs"
              value={form.credentialsSecretRef}
              onChange={(e) => set("credentialsSecretRef", e.target.value)}
            />
          )}
        </FormField>
      </FormSection>

      {/* The typical AWS S3 setup keeps both defaults; MinIO and SeaweedFS
          operators open this. */}
      <FormDisclosure title="Advanced — public endpoint, path-style addressing">
        <FormField
          label="Public endpoint"
          hint="Used in presigned URLs handed to clients. Defaults to the internal endpoint."
        >
          {(control) => (
            <Input
              {...control}
              placeholder="https://s3.example.com"
              className="font-mono text-xs"
              value={form.publicEndpoint}
              onChange={(e) => set("publicEndpoint", e.target.value)}
            />
          )}
        </FormField>
        <label className="flex items-start gap-2 text-sm">
          <Checkbox
            id="be-pathstyle"
            checked={form.forcePathStyle}
            onCheckedChange={(v) => set("forcePathStyle", v === true)}
          />
          <span className="space-y-0.5">
            <span className="font-medium leading-none">
              Force path-style addressing
            </span>
            <span className="block text-xs text-muted-foreground">
              Required for MinIO and SeaweedFS; usually off for AWS S3.
            </span>
          </span>
        </label>
      </FormDisclosure>
    </FormDialog>
  );
}
