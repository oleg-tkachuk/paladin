"use client";

import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { ConnectError } from "@connectrpc/connect";
import { SparklesIcon } from "@heroicons/react/24/outline";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useBackends } from "@/hooks/useBackends";
import { useBuckets } from "@/hooks/useBuckets";
import { useNotification } from "@/components/ui/Notification";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";

// SLUG_RE mirrors backend/internal/api/apiutil/slug.go ValidateTenantSlug.
const SLUG_RE = /^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$/;
// Permissive across UUID versions — server mints v7, accepts any RFC 4122.
const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-7][0-9a-f]{3}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

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

/**
 * Create-tenant dialog, extracted from tenants/page.tsx. Owns its own form
 * state + the cascading backend/bucket data so the list page no longer carries
 * ~6 useState + 3 effects + the 230-line dialog body. Behaviour is unchanged;
 * tenants/page.test.tsx guards the open + validation contract.
 */
export function TenantCreateDialog({
  open,
  onOpenChange,
  createTenant,
}: TenantCreateDialogProps) {
  const { showNotification } = useNotification();
  const [newSlug, setNewSlug] = useState("");
  const [newId, setNewId] = useState("");
  const [newDisplayName, setNewDisplayName] = useState("");
  const [layout, setLayout] = useState<StorageLayout>("shared");
  const [newBackend, setNewBackend] = useState("");
  const [newBucket, setNewBucket] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // A dedicated tenant gets its own provisioned bucket on the chosen backend
  // (name derived server-side), so only the backend is picked — no bucket.
  const dedicated = layout === "dedicated";

  // Backends + buckets feed the cascading Backend → Bucket dropdowns. Both
  // lists are tiny and refreshed on dialog open.
  const { backends } = useBackends(open);
  const { buckets, fetchBuckets } = useBuckets();
  useEffect(() => {
    if (open) void fetchBuckets();
  }, [open, fetchBuckets]);
  const bucketsForBackend = useMemo(
    () => (newBackend ? buckets.filter((b) => b.backendId === newBackend) : []),
    [buckets, newBackend],
  );
  // Default the backend the moment the dialog opens with backends loaded, and
  // drop a bucket that no longer belongs to the chosen backend. Both are
  // render-phase adjust-on-condition (the guards converge in one extra
  // render) — not set-state-in-effect.
  if (open && !newBackend && backends.length > 0) {
    setNewBackend(backends[0].backendId);
  }
  if (
    newBucket &&
    !buckets.some((b) => b.backendId === newBackend && b.bucketId === newBucket)
  ) {
    setNewBucket("");
  }

  const handleCreate = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!SLUG_RE.test(newSlug)) {
      showNotification({
        type: "error",
        title: "Invalid slug",
        message:
          "Slug must be 3–63 chars, kebab-case, starting with a letter and ending alphanumeric.",
      });
      return;
    }
    if (newId && !UUID_RE.test(newId)) {
      showNotification({
        type: "error",
        title: "Invalid Tenant ID",
        message:
          "Tenant ID must be a valid UUID, or leave empty to auto-generate.",
      });
      return;
    }
    if (!newBackend || (!dedicated && !newBucket)) {
      showNotification({
        type: "error",
        title: dedicated ? "Backend required" : "Default location required",
        message: dedicated
          ? "Pick a storage backend. A dedicated bucket is provisioned there automatically."
          : "Pick a storage backend and a bucket. Tenant objects live there by default.",
      });
      return;
    }
    // Dedicated: backend-only ref (trailing slash) — the server derives the
    // bucket name. Shared: the full backend/bucket binding.
    const defaultBucketRef = dedicated
      ? `storageBackends/${newBackend}/buckets/`
      : `storageBackends/${newBackend}/buckets/${newBucket}`;
    try {
      setSubmitting(true);
      const created = await createTenant(
        newSlug,
        newId,
        newDisplayName,
        {},
        defaultBucketRef,
        layout,
      );
      showNotification({
        type: "success",
        title: "Tenant created",
        message: created.displayName || created.slug || created.tenantId,
      });
      setNewSlug("");
      setNewId("");
      setNewDisplayName("");
      setNewBucket("");
      setLayout("shared");
      onOpenChange(false);
    } catch (err) {
      console.error(err);
      const message =
        err instanceof ConnectError
          ? err.rawMessage
          : err instanceof Error
            ? err.message
            : String(err);
      showNotification({ type: "error", title: "Creation failed", message });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={handleCreate}>
          <DialogHeader>
            <DialogTitle>New tenant</DialogTitle>
            <DialogDescription>
              Slug is the human-readable handle and is immutable after creation.
              The default Cedar policy is applied automatically.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="tenant-slug">
                  Slug <span className="text-destructive">*</span>
                </Label>
                {newSlug.length > 0 && (
                  <span
                    className={cn(
                      "text-[11px] font-medium",
                      SLUG_RE.test(newSlug)
                        ? "text-emerald-600 dark:text-emerald-400"
                        : "text-destructive",
                    )}
                  >
                    {SLUG_RE.test(newSlug) ? "valid" : "invalid format"}
                  </span>
                )}
              </div>
              <Input
                id="tenant-slug"
                autoFocus
                placeholder="acme-prod"
                value={newSlug}
                onChange={(e) => setNewSlug(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                3–63 chars, lowercase kebab-case. Used in URLs and Cedar
                policies. <span className="font-medium">Immutable</span> after
                creation.
              </p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="tenant-display-name">
                Display name{" "}
                <span className="text-muted-foreground font-normal">
                  (optional)
                </span>
              </Label>
              <Input
                id="tenant-display-name"
                placeholder={newSlug || "Acme Corporation"}
                value={newDisplayName}
                onChange={(e) => setNewDisplayName(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                Unique, editable. Defaults to slug when empty.
              </p>
            </div>
            {backends.length === 0 && (
              <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs space-y-1">
                <p className="font-medium text-amber-900 dark:text-amber-200">
                  No storage backends registered yet
                </p>
                <p className="text-muted-foreground">
                  Tenants need a backend + bucket to land. Register one before
                  creating the first tenant.
                </p>
                <Link
                  href="/storage-backends"
                  className="inline-flex items-center gap-1 text-amber-700 dark:text-amber-300 hover:underline"
                  onClick={() => onOpenChange(false)}
                >
                  Open Storage Backends →
                </Link>
              </div>
            )}
            {!dedicated &&
              backends.length > 0 &&
              bucketsForBackend.length === 0 &&
              newBackend && (
                <div className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs space-y-1">
                  <p className="font-medium text-amber-900 dark:text-amber-200">
                    No buckets on backend{" "}
                    <span className={T.code}>{newBackend}</span>
                  </p>
                  <p className="text-muted-foreground">
                    Create a bucket on this backend before continuing.
                  </p>
                  <Link
                    href={`/buckets`}
                    className="inline-flex items-center gap-1 text-amber-700 dark:text-amber-300 hover:underline"
                    onClick={() => onOpenChange(false)}
                  >
                    Open Buckets →
                  </Link>
                </div>
              )}
            <div className="space-y-1.5">
              <Label htmlFor="tenant-layout">Storage layout</Label>
              <SelectRoot
                value={layout}
                onValueChange={(v) => setLayout(v as StorageLayout)}
              >
                <SelectTrigger
                  id="tenant-layout"
                  className="w-full min-w-0 *:data-[slot=select-value]:truncate"
                >
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
            </div>
            <div className={dedicated ? "" : "grid grid-cols-2 gap-3"}>
              <div className="min-w-0 space-y-1.5">
                <Label htmlFor="tenant-backend">
                  Storage backend <span className="text-destructive">*</span>
                </Label>
                <SelectRoot
                  value={newBackend}
                  onValueChange={(v) => setNewBackend(v)}
                >
                  <SelectTrigger
                    id="tenant-backend"
                    className="w-full min-w-0 *:data-[slot=select-value]:truncate"
                  >
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
              </div>
              {!dedicated && (
                <div className="min-w-0 space-y-1.5">
                  <Label htmlFor="tenant-bucket">
                    Bucket <span className="text-destructive">*</span>
                  </Label>
                  <SelectRoot
                    value={newBucket}
                    onValueChange={(v) => setNewBucket(v)}
                    disabled={!newBackend || bucketsForBackend.length === 0}
                  >
                    <SelectTrigger
                      id="tenant-bucket"
                      className="w-full min-w-0 *:data-[slot=select-value]:truncate"
                    >
                      <SelectValue
                        placeholder={
                          !newBackend
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
                </div>
              )}
            </div>
            <p className="text-xs text-muted-foreground -mt-2">
              {dedicated ? (
                <>
                  A private bucket is provisioned on{" "}
                  <span className={cn(T.code, "text-[10px]")}>
                    {newBackend || "<backend>"}
                  </span>{" "}
                  and its name is derived from the tenant ID.
                </>
              ) : (
                <>
                  Tenant objects will live under{" "}
                  <span className={cn(T.code, "text-[10px]")}>
                    {newBucket || "<bucket>"}/&lt;tenant_id&gt;/…
                  </span>
                  . Bind cannot be moved without rebinding via the admin RPC.
                </>
              )}
            </p>
            <details className="group rounded-md border border-border/60 bg-muted/30 px-3 py-2">
              <summary className="cursor-pointer select-none text-xs font-medium text-muted-foreground hover:text-foreground">
                Advanced — supply your own UUID
              </summary>
              <div className="space-y-1.5 pt-3">
                <div className="flex items-center justify-between">
                  <Label htmlFor="tenant-id" className="text-xs">
                    Tenant ID
                  </Label>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    className="h-7 text-xs"
                    onClick={() => setNewId(crypto.randomUUID())}
                  >
                    <SparklesIcon className="size-3" />
                    Generate
                  </Button>
                </div>
                <Input
                  id="tenant-id"
                  placeholder="Leave empty — server mints UUIDv7"
                  className="font-mono text-xs"
                  value={newId}
                  onChange={(e) => setNewId(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Any RFC 4122 UUID. Immutable after creation.
                </p>
              </div>
            </details>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => onOpenChange(false)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={
                submitting ||
                !newSlug ||
                !newBackend ||
                (!dedicated && !newBucket)
              }
            >
              {submitting ? "Creating…" : "Create tenant"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
