"use client";

// /storage-backends — platform-admin index of registered S3-compatible
// backends. Buckets FK into this table; Tenants pick one of these
// (paired with a Bucket) at creation as their default binding. Without
// at least one backend row the rest of the platform can't provision
// anything, so this is the first thing an operator hits on a fresh
// install.
//
// Scope: list + create. Edit / delete / RotateCredentials / TestBackend
// live behind a row dropdown when needed; the create form is the
// 80% case for this page.

import React, { useEffect, useMemo, useState } from "react";
import { ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  CloudIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  ServerStackIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useBackends, CreateBackendInput } from "@/hooks/useBackends";
import { StorageKind } from "@/gen/paladin/admin/v1/types_pb";
import { useNotification } from "@/components/ui/Notification";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card } from "@/components/ui/Card";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

// backend_id format mirrors the `backend_id` CHECK on the buckets table
// and the existing `storage.backends.id` config keys (kebab-case, 3..63
// chars, ASCII alnum + '-'). Letting the server reject is fine but a
// client-side hint avoids the round-trip.
const BACKEND_ID_RE = /^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$/;

const KIND_LABELS: Record<number, string> = {
  [StorageKind.AWS_S3]: "AWS S3",
  [StorageKind.S3_COMPATIBLE]: "S3-compatible",
  [StorageKind.GCS]: "GCS",
  [StorageKind.UNSPECIFIED]: "—",
};

export default function StorageBackendsPage() {
  const { backends, loading, fetchBackends, createBackend } = useBackends();
  const { showNotification } = useNotification();

  const [search, setSearch] = useState("");

  const [createOpen, setCreateOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState<CreateBackendInput>({
    backendId: "",
    displayName: "",
    kind: StorageKind.S3_COMPATIBLE,
    endpoint: "",
    publicEndpoint: "",
    region: "",
    forcePathStyle: true,
    credentialsSecretRef: "",
  });

  useEffect(() => {
    fetchBackends();
  }, [fetchBackends]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return backends;
    return backends.filter(
      (b) =>
        b.backendId.toLowerCase().includes(q) ||
        (b.displayName || "").toLowerCase().includes(q) ||
        (b.region || "").toLowerCase().includes(q) ||
        (b.endpoint || "").toLowerCase().includes(q),
    );
  }, [backends, search]);

  const resetForm = () =>
    setForm({
      backendId: "",
      displayName: "",
      kind: StorageKind.S3_COMPATIBLE,
      endpoint: "",
      publicEndpoint: "",
      region: "",
      forcePathStyle: true,
      credentialsSecretRef: "",
    });

  const handleCreate = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!BACKEND_ID_RE.test(form.backendId)) {
      showNotification({
        type: "error",
        title: "Invalid backend ID",
        message:
          "3–63 chars, kebab-case (lowercase alphanumeric + hyphens, alnum on edges).",
      });
      return;
    }
    if (!form.endpoint.trim()) {
      showNotification({
        type: "error",
        title: "Endpoint required",
        message: "Internal endpoint is required (e.g. https://s3.example.com).",
      });
      return;
    }
    if (!form.credentialsSecretRef.trim()) {
      showNotification({
        type: "error",
        title: "Credentials reference required",
        message:
          'Provide a secret-store reference (e.g. "vault://kv/paladin/primary").',
      });
      return;
    }
    try {
      setSubmitting(true);
      const created = await createBackend(form);
      showNotification({
        type: "success",
        title: "Backend registered",
        message: created.displayName || created.backendId,
      });
      resetForm();
      setCreateOpen(false);
    } catch (err) {
      const message =
        err instanceof ConnectError
          ? err.rawMessage
          : err instanceof Error
            ? err.message
            : String(err);
      showNotification({
        type: "error",
        title: "Creation failed",
        message,
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Storage Backends"
        description="Physical S3-compatible targets. Buckets and tenants bind here."
        showDefaultActions={false}
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" />
            New backend
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search by ID, name, region, endpoint…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void fetchBackends()}
          aria-label="Refresh"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[220px]">Backend ID</TableHead>
              <TableHead>Display name</TableHead>
              <TableHead className="hidden md:table-cell">Kind</TableHead>
              <TableHead className="hidden md:table-cell">Region</TableHead>
              <TableHead className="hidden lg:table-cell">Endpoint</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && backends.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={5} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={5} className="h-48 text-center">
                  <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <CloudIcon className="size-10 opacity-40" />
                    <p className="text-sm">
                      {search
                        ? "No backends match your search."
                        : "No storage backends registered yet."}
                    </p>
                    {!search && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setCreateOpen(true)}
                      >
                        <PlusIcon className="size-4" />
                        Register the first backend
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((b) => (
                <TableRow key={b.backendId} className="group">
                  <TableCell>
                    <div className="flex items-center gap-3">
                      <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                        <ServerStackIcon className="size-4" />
                      </div>
                      <span className="font-medium">{b.backendId}</span>
                    </div>
                  </TableCell>
                  <TableCell>
                    {b.displayName || (
                      <span className="text-muted-foreground italic">
                        (unnamed)
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    <Badge variant="outline" className={T.labelTight}>
                      {KIND_LABELS[b.kind] || "—"}
                    </Badge>
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    <span className="font-mono text-xs text-muted-foreground">
                      {b.region || "—"}
                    </span>
                  </TableCell>
                  <TableCell className="hidden lg:table-cell">
                    <span
                      className="font-mono text-[11px] text-muted-foreground truncate"
                      title={b.endpoint}
                    >
                      {b.endpoint || "—"}
                    </span>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent className="max-w-lg">
          <form onSubmit={handleCreate}>
            <DialogHeader>
              <DialogTitle>Register storage backend</DialogTitle>
              <DialogDescription>
                Physical S3-compatible target. Buckets and tenants bind here —
                register the backend before provisioning either.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-4">
              <div className="space-y-1.5">
                <Label htmlFor="be-id">
                  Backend ID <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="be-id"
                  autoFocus
                  placeholder="aws-eu, r2-global, minio-dev"
                  value={form.backendId}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, backendId: e.target.value }))
                  }
                />
                <p className="text-xs text-muted-foreground">
                  3–63 chars, kebab-case. Used in resource names and bucket
                  refs. Immutable.
                </p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="be-display">Display name</Label>
                <Input
                  id="be-display"
                  placeholder={form.backendId || "AWS Frankfurt"}
                  value={form.displayName}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, displayName: e.target.value }))
                  }
                />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label htmlFor="be-kind">Kind</Label>
                  <SelectRoot
                    value={String(form.kind)}
                    onValueChange={(v) =>
                      setForm((f) => ({ ...f, kind: Number(v) as StorageKind }))
                    }
                  >
                    <SelectTrigger id="be-kind">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={String(StorageKind.AWS_S3)}>
                        AWS S3
                      </SelectItem>
                      <SelectItem value={String(StorageKind.S3_COMPATIBLE)}>
                        S3-compatible
                      </SelectItem>
                      <SelectItem value={String(StorageKind.GCS)}>
                        GCS
                      </SelectItem>
                    </SelectContent>
                  </SelectRoot>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="be-region">Region</Label>
                  <Input
                    id="be-region"
                    placeholder="eu-central-1"
                    value={form.region}
                    onChange={(e) =>
                      setForm((f) => ({ ...f, region: e.target.value }))
                    }
                  />
                </div>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="be-endpoint">
                  Internal endpoint <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="be-endpoint"
                  placeholder="https://s3.eu-central-1.amazonaws.com"
                  value={form.endpoint}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, endpoint: e.target.value }))
                  }
                  className="font-mono text-xs"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="be-pub">
                  Public endpoint{" "}
                  <span className="text-muted-foreground font-normal">
                    (optional)
                  </span>
                </Label>
                <Input
                  id="be-pub"
                  placeholder="defaults to internal endpoint"
                  value={form.publicEndpoint}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, publicEndpoint: e.target.value }))
                  }
                  className="font-mono text-xs"
                />
                <p className="text-xs text-muted-foreground">
                  Used in presigned URLs handed to clients.
                </p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="be-secret">
                  Credentials secret reference{" "}
                  <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="be-secret"
                  placeholder="vault://kv/paladin/primary"
                  value={form.credentialsSecretRef}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      credentialsSecretRef: e.target.value,
                    }))
                  }
                  className="font-mono text-xs"
                />
                <p className="text-xs text-muted-foreground">
                  Opaque reference to the secret store. Credentials are never
                  stored or returned.
                </p>
              </div>
              <label className="flex items-start gap-2 text-sm">
                <Checkbox
                  checked={form.forcePathStyle}
                  onCheckedChange={(v) =>
                    setForm((f) => ({ ...f, forcePathStyle: v === true }))
                  }
                  id="be-pathstyle"
                />
                <span className="space-y-0.5">
                  <span className="font-medium leading-none">
                    Force path-style addressing
                  </span>
                  <span className="block text-xs text-muted-foreground">
                    Required for MinIO/SeaweedFS; usually off for AWS S3.
                  </span>
                </span>
              </label>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setCreateOpen(false);
                  resetForm();
                }}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={submitting || !form.backendId || !form.endpoint}
              >
                {submitting ? "Registering…" : "Register backend"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
