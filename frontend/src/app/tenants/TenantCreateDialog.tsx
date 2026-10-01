"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { ConnectError } from "@connectrpc/connect";
import { SparklesIcon } from "@heroicons/react/24/outline";

import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  FormDialog,
  FormDisclosure,
  FormField,
  FormRow,
  FormSection,
} from "@/components/ui/form-dialog";
import { useBackends } from "@/hooks/useBackends";
import { useBuckets } from "@/hooks/useBuckets";
import { failedRead, ListLoadError } from "@/components/ui/ListLoadError";
import { useNotification } from "@/components/ui/Notification";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";

// SLUG_RE mirrors backend/internal/api/apiutil/slug.go ValidateTenantSlug.
const SLUG_RE = /^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$/;
// Permissive across UUID versions — server mints v7, accepts any RFC 4122.
const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-7][0-9a-f]{3}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export const SLUG_ERROR =
  "3–63 characters: lowercase letters, digits and hyphens, starting with a letter.";
export const TENANT_ID_ERROR = "Not a UUID. Leave empty to have one minted.";

type CreateTenantFn = (
  slug: string,
  tenantId: string,
  displayName: string,
  labels: Record<string, string>,
  defaultBucket: string,
  storageLayout: string,
) => Promise<Tenant>;

type StorageLayout = "shared" | "dedicated";

interface TenantCreateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  createTenant: CreateTenantFn;
}

/** A link out of the dialog, for a prerequisite it cannot satisfy itself. */
function Prerequisite({
  children,
  href,
  action,
  onLeave,
}: {
  children: React.ReactNode;
  href: string;
  action: string;
  onLeave: () => void;
}) {
  return (
    <div className="space-y-1 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs">
      <p className="text-amber-900 dark:text-amber-200">{children}</p>
      <Link
        href={href}
        className="inline-flex items-center gap-1 text-amber-700 hover:underline dark:text-amber-300"
        onClick={onLeave}
      >
        {action} →
      </Link>
    </div>
  );
}

export function TenantCreateDialog({
  open,
  onOpenChange,
  createTenant,
}: TenantCreateDialogProps) {
  const { showNotification } = useNotification();
  const [slug, setSlug] = useState("");
  const [tenantId, setTenantId] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [layout, setLayout] = useState<StorageLayout>("shared");
  const [backendId, setBackendId] = useState("");
  const [bucketId, setBucketId] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // A dedicated tenant gets its own provisioned bucket on the chosen backend
  // (name derived server-side), so only the backend is picked — no bucket.
  const dedicated = layout === "dedicated";

  const { backends, error: backendsError, fetchBackends } = useBackends(open);
  const { buckets, error: bucketsError, fetchBuckets } = useBuckets();
  // A failed read leaves its list empty, which every check below would read as
  // "none registered" and answer by sending the operator off to create one.
  const backendsFailed = failedRead(backendsError, fetchBackends);
  const bucketsFailed = failedRead(bucketsError, fetchBuckets);
  useEffect(() => {
    if (open) void fetchBuckets();
  }, [open, fetchBuckets]);
  const bucketsForBackend = useMemo(
    () => (backendId ? buckets.filter((b) => b.backendId === backendId) : []),
    [buckets, backendId],
  );
  // Render-phase adjust-on-condition (each guard converges in one extra
  // render): default the backend once backends load, drop a bucket that left
  // the chosen backend, and pick the bucket when there is only one to pick.
  if (open && !backendId && backends.length > 0) {
    setBackendId(backends[0].backendId);
  }
  if (bucketId && !bucketsForBackend.some((b) => b.bucketId === bucketId)) {
    setBucketId("");
  }
  if (!bucketId && bucketsForBackend.length === 1) {
    setBucketId(bucketsForBackend[0].bucketId);
  }

  const slugError = slug && !SLUG_RE.test(slug) ? SLUG_ERROR : null;
  const idError = tenantId && !UUID_RE.test(tenantId) ? TENANT_ID_ERROR : null;

  const blockedReason = !slug
    ? "Enter a slug to continue."
    : slugError
      ? "Fix the slug to continue."
      : backendsFailed
        ? "Backends could not be loaded."
        : backends.length === 0
          ? "Register a storage backend first."
          : !backendId
            ? "Pick a storage backend."
            : !dedicated && bucketsFailed
              ? "Buckets could not be loaded."
              : !dedicated && !bucketId
                ? "Pick a bucket to continue."
                : idError
                  ? "Fix the tenant ID, or clear it."
                  : null;

  const reset = () => {
    setSlug("");
    setTenantId("");
    setDisplayName("");
    setBucketId("");
    setLayout("shared");
    setSubmitError(null);
  };

  const handleCreate = async () => {
    // Dedicated: backend-only ref (trailing slash) — the server derives the
    // bucket name. Shared: the full backend/bucket binding.
    const defaultBucketRef = dedicated
      ? `storageBackends/${backendId}/buckets/`
      : `storageBackends/${backendId}/buckets/${bucketId}`;
    setSubmitError(null);
    try {
      setSubmitting(true);
      const created = await createTenant(
        slug,
        tenantId,
        displayName,
        {},
        defaultBucketRef,
        layout,
      );
      showNotification({
        type: "success",
        title: "Tenant created",
        message: created.displayName || created.slug || created.tenantId,
      });
      reset();
      onOpenChange(false);
    } catch (err) {
      console.error(err);
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

  const leave = () => onOpenChange(false);

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="New tenant"
      description="A workspace with its own storage binding and Cedar policy."
      width="lg"
      onSubmit={() => void handleCreate()}
      submitLabel="Create tenant"
      submittingLabel="Creating…"
      submitting={submitting}
      blockedReason={blockedReason}
      error={submitError}
    >
      <FormSection title="Identity">
        <FormRow>
          <FormField
            label="Slug"
            required
            error={slugError}
            hint="Used in URLs and Cedar policies. Cannot be changed later."
            aside={
              slug && !slugError ? (
                <span className="text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
                  valid
                </span>
              ) : null
            }
          >
            {(control) => (
              <Input
                {...control}
                autoFocus
                placeholder="acme-prod"
                value={slug}
                onChange={(e) => setSlug(e.target.value)}
              />
            )}
          </FormField>
          <FormField label="Display name" hint="Defaults to the slug.">
            {(control) => (
              <Input
                {...control}
                placeholder={slug || "Acme Corporation"}
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
      </FormSection>

      <FormSection title="Storage">
        {!backendsFailed && backends.length === 0 ? (
          <Prerequisite
            href="/storage-backends"
            action="Open Storage Backends"
            onLeave={leave}
          >
            A tenant needs a storage backend, and none is registered.
          </Prerequisite>
        ) : null}
        <FormField
          label="Layout"
          hint={
            dedicated
              ? "A private bucket is provisioned on the backend, named from the tenant ID."
              : "Objects live under <bucket>/<tenant_id>/ in a bucket other tenants may share."
          }
        >
          {(control) => (
            <SelectRoot
              value={layout}
              onValueChange={(v) => setLayout(v as StorageLayout)}
            >
              <SelectTrigger {...control}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="shared">
                  Shared — bind to an existing bucket
                </SelectItem>
                <SelectItem value="dedicated">
                  Dedicated — provision a private bucket
                </SelectItem>
              </SelectContent>
            </SelectRoot>
          )}
        </FormField>
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
                <SelectRoot value={backendId} onValueChange={setBackendId}>
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
                      <SelectItem key={b.backendId} value={b.backendId}>
                        {b.displayName
                          ? `${b.displayName} (${b.backendId})`
                          : b.backendId}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </SelectRoot>
              )
            }
          </FormField>
          {!dedicated ? (
            <FormField label="Bucket" required>
              {(control) =>
                bucketsFailed ? (
                  <ListLoadError
                    variant="inline"
                    what="Buckets"
                    reason={bucketsFailed.reason}
                    onRetry={bucketsFailed.retry}
                  />
                ) : (
                  <SelectRoot
                    value={bucketId}
                    onValueChange={setBucketId}
                    disabled={!backendId || bucketsForBackend.length === 0}
                  >
                    <SelectTrigger {...control}>
                      <SelectValue
                        placeholder={
                          !backendId
                            ? "Pick backend first"
                            : bucketsForBackend.length === 0
                              ? "No buckets on this backend"
                              : "Pick bucket"
                        }
                      />
                    </SelectTrigger>
                    <SelectContent>
                      {bucketsForBackend.map((b) => (
                        <SelectItem key={b.bucketId} value={b.bucketId}>
                          {b.bucketId}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </SelectRoot>
                )
              }
            </FormField>
          ) : null}
        </FormRow>
        {!dedicated &&
        !bucketsFailed &&
        backendId &&
        backends.length > 0 &&
        bucketsForBackend.length === 0 ? (
          <Prerequisite href="/buckets" action="Open Buckets" onLeave={leave}>
            Backend <span className={T.code}>{backendId}</span> has no bucket to
            bind to yet.
          </Prerequisite>
        ) : null}
      </FormSection>

      <FormDisclosure title="Advanced">
        <FormField
          label="Tenant ID"
          error={idError}
          hint="Any RFC 4122 UUID. Empty mints a UUIDv7. Cannot be changed later."
          aside={
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="h-6 text-xs"
              onClick={() => setTenantId(crypto.randomUUID())}
            >
              <SparklesIcon className="size-3" />
              Generate
            </Button>
          }
        >
          {(control) => (
            <Input
              {...control}
              placeholder="Server-minted"
              className={cn("font-mono text-xs")}
              value={tenantId}
              onChange={(e) => setTenantId(e.target.value)}
            />
          )}
        </FormField>
      </FormDisclosure>
    </FormDialog>
  );
}
