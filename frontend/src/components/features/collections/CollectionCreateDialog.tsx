"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { ConnectError } from "@connectrpc/connect";

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
import { useBuckets } from "@/hooks/useBuckets";
import {
  type FailedRead,
  failedRead,
  ListLoadError,
} from "@/components/ui/ListLoadError";
import { T } from "@/lib/ui/typography";

interface CollectionCreateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /**
   * Backends that can receive a Collection — enabled and writable. A disabled
   * or draining one would leave a dialog that cannot be completed.
   */
  backends: string[];
  /** Set when the backend list failed to load: `backends` is then unknown. */
  backendsFailed?: FailedRead | null;
  /** Where to send someone whose backend has no bucket yet. */
  bucketsHref: string;
  createCollection: (
    collection: string,
    displayName: string,
    backendId: string,
    bucketId: string,
  ) => Promise<unknown>;
  onCreated?: () => void;
}

/**
 * New Collection — one dialog for the cross-tenant list and a tenant's own,
 * which each carried a copy. The Collection is created in the signed-in
 * tenant (useCollections), which is why the tenant page offers it only there.
 */
export function CollectionCreateDialog({
  open,
  onOpenChange,
  backends,
  backendsFailed = null,
  bucketsHref,
  createCollection,
  onCreated,
}: CollectionCreateDialogProps) {
  const { showNotification } = useNotification();
  const { buckets, error: bucketsError, fetchBuckets } = useBuckets();
  const bucketsFailed = failedRead(bucketsError, fetchBuckets);
  const [backendId, setBackendId] = useState("");
  const [bucketId, setBucketId] = useState("");
  const [path, setPath] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  useEffect(() => {
    if (open) void fetchBuckets();
  }, [open, fetchBuckets]);

  // Default to a backend that has buckets when one exists — a Collection binds
  // to a bucket, so an empty backend is a dialog that cannot be submitted
  // until the operator works out that the backend, not the form, is the
  // problem.
  const defaultBackend = useMemo(
    () =>
      backends.find((id) => buckets.some((b) => b.backendId === id)) ??
      backends[0] ??
      "",
    [backends, buckets],
  );
  const bucketsForBackend = useMemo(
    () => buckets.filter((b) => b.backendId === backendId),
    [buckets, backendId],
  );
  // Render-phase adjust-on-condition, each converging in one extra render.
  if (open && !backendId && defaultBackend) {
    setBackendId(defaultBackend);
  }
  if (bucketId && !bucketsForBackend.some((b) => b.bucketId === bucketId)) {
    setBucketId("");
  }
  if (!bucketId && bucketsForBackend.length > 0) {
    setBucketId(bucketsForBackend[0].bucketId);
  }

  // Failed reads are checked before empty ones: both leave the list empty, and
  // only one of them means there is nothing to choose.
  const blockedReason = backendsFailed
    ? "Backends could not be loaded."
    : bucketsFailed
      ? "Buckets could not be loaded."
      : backends.length === 0
        ? "No backend can take a Collection."
        : !bucketId
          ? "Pick a bucket to continue."
          : !path
            ? "Enter a path to continue."
            : null;

  const handleCreate = async () => {
    setSubmitError(null);
    try {
      setSubmitting(true);
      await createCollection(path, displayName, backendId, bucketId);
      showNotification({
        type: "success",
        title: "Collection created",
        message: `${path} → s3://${bucketId}/`,
      });
      setPath("");
      setDisplayName("");
      onOpenChange(false);
      onCreated?.();
    } catch (err) {
      setSubmitError(
        err instanceof ConnectError
          ? err.rawMessage
          : err instanceof Error
            ? err.message
            : "Failed to create the Collection.",
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="New Collection"
      description="A path in your tenant, stored in one S3 bucket."
      width="lg"
      onSubmit={() => void handleCreate()}
      submitLabel="Create Collection"
      submittingLabel="Provisioning…"
      submitting={submitting}
      blockedReason={blockedReason}
      error={submitError}
    >
      <FormSection title="Collection">
        <FormRow>
          <FormField
            label="Path"
            required
            hint={
              <>
                Slash-separated, each segment lowercase kebab-case —{" "}
                <span className={T.code}>invoices/2026/q1</span>.
              </>
            }
          >
            {(control) => (
              <Input
                {...control}
                autoFocus
                maxLength={63}
                placeholder="invoices/2026/q1"
                className="font-mono text-xs"
                value={path}
                onChange={(e) => setPath(e.target.value.toLowerCase())}
              />
            )}
          </FormField>
          <FormField label="Display name">
            {(control) => (
              <Input
                {...control}
                placeholder="Production assets"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
              />
            )}
          </FormField>
        </FormRow>
      </FormSection>

      <FormSection title="Storage">
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
                  onValueChange={(v) => {
                    setBackendId(v);
                    setBucketId("");
                  }}
                  disabled={backends.length === 0}
                >
                  <SelectTrigger {...control}>
                    <SelectValue
                      placeholder={
                        backends.length === 0
                          ? "No usable backend"
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
                  disabled={bucketsForBackend.length === 0}
                >
                  <SelectTrigger {...control}>
                    <SelectValue
                      placeholder={
                        bucketsForBackend.length === 0
                          ? "No buckets on this backend"
                          : "Pick bucket"
                      }
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {bucketsForBackend.map((b) => (
                      <SelectItem key={b.bucketId} value={b.bucketId}>
                        <span className="font-mono">{b.bucketId}</span>
                        {b.displayName ? (
                          <span className="text-muted-foreground">
                            {" "}
                            — {b.displayName}
                          </span>
                        ) : null}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </SelectRoot>
              )
            }
          </FormField>
        </FormRow>
        {backendId && !bucketsFailed && bucketsForBackend.length === 0 ? (
          <div className="space-y-1 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs">
            <p className="text-amber-900 dark:text-amber-200">
              Backend <span className={T.code}>{backendId}</span> has no bucket
              yet.
            </p>
            <Link
              href={bucketsHref}
              onClick={() => onOpenChange(false)}
              className="inline-flex items-center gap-1 text-amber-700 hover:underline dark:text-amber-300"
            >
              Open Buckets →
            </Link>
          </div>
        ) : null}
      </FormSection>
    </FormDialog>
  );
}
