"use client";

import { useEffect, useMemo, useState } from "react";
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
import { useBuckets } from "@/hooks/useBuckets";
import {
  type FailedRead,
  failedRead,
  ListLoadError,
} from "@/components/ui/ListLoadError";
import { T } from "@/lib/ui/typography";
import { errorMessage } from "@/hooks/errorContract";

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
    cedarPolicy: string,
    publicRead: { cacheControl: string } | null,
  ) => Promise<unknown>;
  onCreated?: () => void;
}

/**
 * New Collection — one dialog for the cross-tenant list and a tenant's own,
 * which each carried a copy. The Collection is created in the signed-in
 * tenant (useCollections), which is why the tenant page offers it only there.
 *
 * A Collection's access is its bucket's (ADR-0027): bound to a public bucket
 * it is public, and the dialog says so before it is created. A private
 * bucket is picked by default, so publishing is always a choice.
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
  const [cacheControl, setCacheControl] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  // The default backend waits for the buckets: chosen before they arrive, it
  // was the first backend whatever it held, often one with no bucket at all.
  const [bucketsRead, setBucketsRead] = useState(false);
  useEffect(() => {
    if (open) void fetchBuckets().finally(() => setBucketsRead(true));
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
  if (open && !backendId && defaultBackend && bucketsRead) {
    setBackendId(defaultBackend);
  }
  if (bucketId && !bucketsForBackend.some((b) => b.bucketId === bucketId)) {
    setBucketId("");
  }
  if (!bucketId && bucketsForBackend.length > 0) {
    setBucketId(
      (bucketsForBackend.find((b) => !b.publicRead) ?? bucketsForBackend[0])
        .bucketId,
    );
  }
  // An error answers the request that was sent; once the form changes it no
  // longer describes what Create would send.
  const draft = JSON.stringify([
    backendId,
    bucketId,
    path,
    displayName,
    cacheControl,
  ]);
  const [erroredDraft, setErroredDraft] = useState(draft);
  if (submitError && draft !== erroredDraft) {
    setSubmitError(null);
  }
  const publicBucket =
    bucketsForBackend.find((b) => b.bucketId === bucketId)?.publicRead ?? false;

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
      await createCollection(
        path,
        displayName,
        backendId,
        bucketId,
        "",
        publicBucket ? { cacheControl } : null,
      );
      showNotification({
        type: "success",
        title: "Collection created",
        message: `${path} → s3://${bucketId}/`,
      });
      setPath("");
      setDisplayName("");
      setCacheControl("");
      onOpenChange(false);
      onCreated?.();
    } catch (err) {
      setErroredDraft(draft);
      setSubmitError(errorMessage(err, "Failed to create the Collection."));
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
                        {b.publicRead ? (
                          <span className="text-warning"> · public</span>
                        ) : null}
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
        {publicBucket && (
          <>
            <p
              role="note"
              className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
            >
              <span className={T.code}>{bucketId}</span> is public: anyone with
              an object&apos;s URL will read it, unsigned. Paladin names every
              object, and a delete is permanent.
            </p>
            <FormField
              label="Cache-Control"
              hint="Stored with every object. Empty uses public, max-age=31536000, immutable. Cannot change later."
            >
              {(control) => (
                <Input
                  {...control}
                  placeholder="public, max-age=31536000, immutable"
                  className="font-mono text-xs"
                  value={cacheControl}
                  onChange={(e) => setCacheControl(e.target.value)}
                />
              )}
            </FormField>
          </>
        )}
        {backendId && !bucketsFailed && bucketsForBackend.length === 0 ? (
          <div className="space-y-1 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs">
            <p className="text-warning">
              Backend <span className={T.code}>{backendId}</span> has no bucket
              yet.
            </p>
            <Link
              href={bucketsHref}
              onClick={() => onOpenChange(false)}
              className="inline-flex items-center gap-1 text-warning hover:underline"
            >
              Open Buckets →
            </Link>
          </div>
        ) : null}
      </FormSection>
    </FormDialog>
  );
}
