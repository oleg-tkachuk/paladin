"use client";

import { useState } from "react";

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
  FormField,
  FormRow,
  FormSection,
} from "@/components/ui/form-dialog";
import { useNotification } from "@/components/ui/Notification";
import { bucketNameError } from "@/lib/bucketName";
import { type FailedRead, ListLoadError } from "@/components/ui/ListLoadError";
import { errorMessage } from "@/hooks/errorContract";

interface BucketCreateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Backend ids to offer; the first is picked when the dialog opens. */
  backends: string[];
  /** Set when the backend list failed to load: `backends` is then unknown. */
  backendsFailed?: FailedRead | null;
  createBucket: (
    backendId: string,
    bucketId: string,
    displayName: string,
    region: string,
  ) => Promise<unknown>;
}

/**
 * New S3 bucket — one dialog for the cross-tenant list and a tenant's own,
 * which each carried a copy. The bucket is created shared (no owner tenant)
 * from either page; the description says so rather than implying otherwise.
 */
export function BucketCreateDialog({
  open,
  onOpenChange,
  backends,
  backendsFailed = null,
  createBucket,
}: BucketCreateDialogProps) {
  const { showNotification } = useNotification();
  const [backendId, setBackendId] = useState("");
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [region, setRegion] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Render-phase adjust-on-condition: default the backend once they load.
  if (open && !backendId && backends.length > 0) {
    setBackendId(backends[0]);
  }

  // Shown only once something is typed: an empty field is not yet wrong.
  const nameError = name ? bucketNameError(name) : null;
  // A failed read is checked before an empty one: both leave `backends` empty,
  // and only one of them means there is nothing to register a bucket on.
  const blockedReason = backendsFailed
    ? "Backends could not be loaded."
    : backends.length === 0
      ? "Register a storage backend first."
      : !name
        ? "Enter a bucket name to continue."
        : nameError
          ? "Fix the bucket name to continue."
          : null;

  const handleCreate = async () => {
    setSubmitError(null);
    try {
      setSubmitting(true);
      await createBucket(backendId, name, displayName, region);
      showNotification({
        type: "success",
        title: "Bucket created",
        message: `${name} (backend ${backendId})`,
      });
      setName("");
      setDisplayName("");
      setRegion("");
      onOpenChange(false);
    } catch (err) {
      setSubmitError(errorMessage(err, "Failed to create bucket."));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="New S3 bucket"
      description="Created on the backend and shared: any tenant can bind to it."
      width="lg"
      onSubmit={() => void handleCreate()}
      submitLabel="Create bucket"
      submittingLabel="Provisioning…"
      submitting={submitting}
      blockedReason={blockedReason}
      error={submitError}
    >
      <FormSection>
        <FormRow>
          <FormField label="Backend" required>
            {(control) =>
              backendsFailed ? (
                <ListLoadError
                  variant="inline"
                  what="Backends"
                  reason={backendsFailed.reason}
                  onRetry={backendsFailed.retry}
                />
              ) : (
                <SelectRoot
                  value={backendId}
                  onValueChange={setBackendId}
                  disabled={backends.length === 0}
                >
                  <SelectTrigger {...control}>
                    <SelectValue
                      placeholder={
                        backends.length === 0
                          ? "No backends registered"
                          : "Pick backend"
                      }
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {backends.map((b) => (
                      <SelectItem key={b} value={b}>
                        {b}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </SelectRoot>
              )
            }
          </FormField>
          <FormField
            label="Bucket name"
            required
            error={nameError}
            hint="The S3 name. Lowercase; cannot be changed later."
          >
            {(control) => (
              <Input
                {...control}
                autoFocus
                placeholder="paladin-primary"
                className="font-mono text-xs"
                value={name}
                onChange={(e) => setName(e.target.value.toLowerCase())}
              />
            )}
          </FormField>
        </FormRow>
        <FormRow>
          <FormField label="Display name" hint="Shown in the console.">
            {(control) => (
              <Input
                {...control}
                placeholder="Friendly label"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
              />
            )}
          </FormField>
          <FormField label="Region" hint="Empty uses the backend's region.">
            {(control) => (
              <Input
                {...control}
                placeholder="us-east-1"
                className="font-mono text-xs"
                value={region}
                onChange={(e) => setRegion(e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
      </FormSection>
    </FormDialog>
  );
}
