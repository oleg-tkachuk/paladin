"use client";

import { useState } from "react";
import Link from "next/link";

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
import { errorMessage, errorReason } from "@/hooks/errorContract";
import type { PublicReadSettings } from "@/hooks/useBuckets";
import { ChipInput } from "@/components/ui/ChipInput";
import { Switch } from "@/components/ui/switch";
import { ErrorReason } from "@/gen/paladin/common/v1/error_reason_pb";
import {
  StorageFeature,
  type StorageBackend,
} from "@/gen/paladin/admin/v1/types_pb";
import { supports } from "@/lib/storageFeatures";

interface BucketCreateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /**
   * Backends to offer. The first that can hold a bucket is picked when the
   * dialog opens; one that cannot is listed, held, with the reason.
   */
  backends: Pick<
    StorageBackend,
    "backendId" | "features" | "enabled" | "declared"
  >[];
  /** Set when the backend list failed to load: `backends` is then unknown. */
  backendsFailed?: FailedRead | null;
  createBucket: (
    backendId: string,
    bucketId: string,
    displayName: string,
    region: string,
    provisionOnBackend: boolean,
    publicRead: PublicReadSettings | null,
  ) => Promise<unknown>;
}

/**
 * Why the server refuses a bucket on this backend, or null when it takes one:
 * a disabled backend, or one only registered through the API — the data plane
 * builds storage clients from storage.backends alone.
 */
export function bucketBackendBlock(
  backend: Pick<StorageBackend, "enabled" | "declared">,
): string | null {
  if (!backend.enabled) return "disabled";
  if (!backend.declared) return "not in the server's storage.backends";
  return null;
}

/** What to do next, for the refusals that have one. */
const REASON_HINTS: Partial<Record<ErrorReason, string>> = {
  [ErrorReason.BACKEND_FEATURE_UNSUPPORTED]:
    " Run Test connectivity on the backend's page to probe it.",
  [ErrorReason.BUCKET_EXISTS_ON_BACKEND]:
    " To manage it, turn on Register an existing bucket.",
  [ErrorReason.BUCKET_NOT_ON_BACKEND]:
    " Turn off Register an existing bucket to create it.",
};

/** The backend page, where its features are probed and shown. */
const backendHref = (backendId: string) =>
  `/storage-backends/${encodeURIComponent(backendId)}`;

/**
 * New S3 bucket — one dialog for the cross-tenant list and a tenant's own,
 * which each carried a copy. The bucket is created shared (no owner tenant)
 * from either page; the description says so rather than implying otherwise.
 *
 * It may be created public (ADR-0027): anyone reads its objects by URL. That
 * is offered only on a backend whose probe found anonymous reads enforced,
 * and asks for the content types the bucket will serve.
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
  const [publicRead, setPublicRead] = useState(false);
  // Register a bucket the backend already holds instead of creating one
  // (ADR-0028). Such a bucket is never public.
  const [adopt, setAdopt] = useState(false);
  const [allowedTypes, setAllowedTypes] = useState<string[]>([]);
  const [baseUrl, setBaseUrl] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Render-phase adjust-on-condition: default the backend once they load, to
  // one that can hold a bucket when any can.
  if (open && !backendId && backends.length > 0) {
    setBackendId(
      (backends.find((b) => !bucketBackendBlock(b)) ?? backends[0]).backendId,
    );
  }
  const backend = backends.find((b) => b.backendId === backendId);
  const backendBlock = backend ? bucketBackendBlock(backend) : null;

  // An error answers the request that was sent; once the form changes it no
  // longer describes what Create would send.
  const draft = JSON.stringify([
    backendId,
    name,
    displayName,
    region,
    publicRead,
    adopt,
    allowedTypes,
    baseUrl,
  ]);
  const [erroredDraft, setErroredDraft] = useState(draft);
  if (submitError && draft !== erroredDraft) {
    setSubmitError(null);
  }
  const canPublish =
    backend !== undefined &&
    supports(backend, StorageFeature.ANONYMOUS_READ_POLICY);
  // A backend switched to one that cannot publish, or an adopted bucket,
  // takes the setting with it.
  if (publicRead && (!canPublish || adopt)) {
    setPublicRead(false);
  }

  // Shown only once something is typed: an empty field is not yet wrong.
  const nameError = name ? bucketNameError(name) : null;
  // A failed read is checked before an empty one: both leave `backends` empty,
  // and only one of them means there is nothing to register a bucket on.
  const blockedReason = backendsFailed
    ? "Backends could not be loaded."
    : backends.length === 0
      ? "Register a storage backend first."
      : backendBlock
        ? `Backend ${backendId} is ${backendBlock}; pick another.`
        : !name
          ? "Enter a bucket name to continue."
          : nameError
            ? "Fix the bucket name to continue."
            : publicRead && allowedTypes.length === 0
              ? "List the content types a public bucket serves."
              : null;

  const handleCreate = async () => {
    setSubmitError(null);
    try {
      setSubmitting(true);
      await createBucket(
        backendId,
        name,
        displayName,
        region,
        !adopt,
        publicRead ? { allowedContentTypes: allowedTypes, baseUrl } : null,
      );
      showNotification({
        type: "success",
        title: "Bucket created",
        message: `${name} (backend ${backendId})`,
      });
      setName("");
      setDisplayName("");
      setRegion("");
      setPublicRead(false);
      setAdopt(false);
      setAllowedTypes([]);
      setBaseUrl("");
      onOpenChange(false);
    } catch (err) {
      setErroredDraft(draft);
      const message = errorMessage(err, "Failed to create bucket.");
      setSubmitError(`${message}${REASON_HINTS[errorReason(err)] ?? ""}`);
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
                    {backends.map((b) => {
                      const block = bucketBackendBlock(b);
                      return (
                        <SelectItem
                          key={b.backendId}
                          value={b.backendId}
                          disabled={block !== null}
                        >
                          {block ? `${b.backendId} — ${block}` : b.backendId}
                        </SelectItem>
                      );
                    })}
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
        <FormField
          label="Register an existing bucket"
          hint={
            adopt
              ? "Paladin manages a bucket the backend already holds. It never deletes it there, and it cannot be public."
              : "Off: Paladin creates the bucket, and refuses a name the backend already holds."
          }
        >
          {(control) => (
            <Switch
              id={control.id}
              aria-describedby={control["aria-describedby"]}
              checked={adopt}
              onCheckedChange={setAdopt}
            />
          )}
        </FormField>
        <FormField
          label="Public read"
          hint={
            canPublish ? (
              "Anyone with an object's URL reads it, unsigned. Fixed at creation."
            ) : (
              <>
                This backend has not shown it enforces anonymous reads.{" "}
                {backendId && (
                  <Link className="underline" href={backendHref(backendId)}>
                    Probe its features
                  </Link>
                )}
              </>
            )
          }
        >
          {(control) => (
            <Switch
              id={control.id}
              aria-describedby={control["aria-describedby"]}
              checked={publicRead}
              disabled={!canPublish || adopt}
              onCheckedChange={setPublicRead}
            />
          )}
        </FormField>
        {publicRead && (
          <FormRow>
            <FormField
              label="Allowed content types"
              required
              hint="What the bucket serves, e.g. image/jpeg. Nothing a browser runs: no HTML, SVG, XML or script."
            >
              {(control) => (
                <ChipInput
                  id={control.id}
                  values={allowedTypes}
                  onChange={setAllowedTypes}
                  placeholder="image/webp"
                />
              )}
            </FormField>
            <FormField
              label="CDN base URL"
              hint="Where a CDN serves the bucket. Empty uses the backend's public endpoint. Consumers store URLs built on it, so it cannot change."
            >
              {(control) => (
                <Input
                  {...control}
                  placeholder="https://cdn.example.com"
                  className="font-mono text-xs"
                  value={baseUrl}
                  onChange={(e) => setBaseUrl(e.target.value.trim())}
                />
              )}
            </FormField>
          </FormRow>
        )}
      </FormSection>
    </FormDialog>
  );
}
