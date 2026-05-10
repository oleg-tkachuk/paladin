"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  ArrowPathIcon,
  ArrowRightIcon,
  ArrowsUpDownIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  BuildingOfficeIcon,
  EllipsisHorizontalIcon,
  MagnifyingGlassIcon,
  PencilSquareIcon,
  PlusIcon,
  SparklesIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { ConnectError } from "@connectrpc/connect";

import { PageHeader } from "@/components/layout/PageHeader";
import { useTenants } from "@/hooks/useTenants";
import { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { useNotification } from "@/components/ui/Notification";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/Card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

type SortColumn = "tenantId" | "displayName";
type SortDirection = "asc" | "desc" | null;

interface SortState {
  column: SortColumn | null;
  direction: SortDirection;
}

function nextSort(prev: SortState, column: SortColumn): SortState {
  if (prev.column !== column) return { column, direction: "asc" };
  if (prev.direction === "asc") return { column, direction: "desc" };
  if (prev.direction === "desc") return { column: null, direction: null };
  return { column, direction: "asc" };
}

function SortHeader({
  label,
  column,
  current,
  onSort,
  className,
}: {
  label: string;
  column: SortColumn;
  current: SortState;
  onSort: (c: SortColumn) => void;
  className?: string;
}) {
  const active = current.column === column && current.direction !== null;
  const Icon = !active
    ? ArrowsUpDownIcon
    : current.direction === "asc"
      ? ArrowUpIcon
      : ArrowDownIcon;

  return (
    <button
      type="button"
      onClick={() => onSort(column)}
      className={cn(
        "inline-flex items-center gap-1 text-xs font-medium uppercase tracking-wider transition-colors",
        active
          ? "text-foreground"
          : "text-muted-foreground hover:text-foreground",
        className,
      )}
    >
      {label}
      <Icon className="size-3.5 opacity-70" />
    </button>
  );
}

// UUID format check is permissive across versions (v1/v4/v7) — server
// generates v7 by default but accepts any RFC 4122 UUID from clients.
const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-7][0-9a-f]{3}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

// SLUG_RE mirrors backend/internal/api/v1/apiutil/slug.go ValidateTenantSlug.
// Keep in lockstep with the server-side regex.
const SLUG_RE = /^[a-z]([a-z0-9-]{1,61}[a-z0-9])?$/;

export default function TenantsPage() {
  const {
    tenants,
    loading,
    fetchTenants,
    createTenant,
    updateTenantMetadata,
    deleteTenant,
  } = useTenants();
  const { showNotification } = useNotification();

  // ─── search + sort ────────────────────────────────────────────────────────
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<SortState>({
    column: null,
    direction: null,
  });
  const handleSort = useCallback(
    (column: SortColumn) => setSort((prev) => nextSort(prev, column)),
    [],
  );

  // ─── create ───────────────────────────────────────────────────────────────
  // Phase 0 contract: slug is required, tenant_id is optional (server
  // mints UUIDv7 when empty), display_name is optional (defaults to
  // slug). Both slug and display_name are unique across tenants.
  const [createOpen, setCreateOpen] = useState(false);
  const [newSlug, setNewSlug] = useState("");
  const [newId, setNewId] = useState("");
  const [newDisplayName, setNewDisplayName] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // ─── edit ─────────────────────────────────────────────────────────────────
  const [editing, setEditing] = useState<Tenant | null>(null);
  const [editDisplayName, setEditDisplayName] = useState("");

  // ─── delete ───────────────────────────────────────────────────────────────
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  useEffect(() => {
    fetchTenants();
  }, [fetchTenants]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    let list = tenants;
    if (q) {
      list = list.filter(
        (t) =>
          t.tenantId.toLowerCase().includes(q) ||
          (t.displayName || "").toLowerCase().includes(q),
      );
    }
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      list = [...list].sort((a, b) => {
        const va =
          sort.column === "tenantId"
            ? a.tenantId
            : (a.displayName || a.tenantId).toLowerCase();
        const vb =
          sort.column === "tenantId"
            ? b.tenantId
            : (b.displayName || b.tenantId).toLowerCase();
        return va < vb ? -dir : va > vb ? dir : 0;
      });
    }
    return list;
  }, [tenants, search, sort]);

  const handleCreate = async (e?: React.FormEvent) => {
    e?.preventDefault();
    // Slug is required and must match the kebab-case format the
    // server validates against (mirrors apiutil.ValidateTenantSlug).
    if (!SLUG_RE.test(newSlug)) {
      showNotification({
        type: "error",
        title: "Invalid slug",
        message:
          "Slug must be 3–63 chars, kebab-case, starting with a letter and ending alphanumeric.",
      });
      return;
    }
    // tenant_id is optional. If supplied, validate UUID format
    // client-side so the server's INVALID_ARGUMENT round-trip isn't
    // the first signal of a typo.
    if (newId && !UUID_RE.test(newId)) {
      showNotification({
        type: "error",
        title: "Invalid Tenant ID",
        message:
          "Tenant ID must be a valid UUID, or leave empty to auto-generate.",
      });
      return;
    }
    try {
      setSubmitting(true);
      const created = await createTenant(newSlug, newId, newDisplayName);
      showNotification({
        type: "success",
        title: "Tenant created",
        message: created.displayName || created.slug || created.tenantId,
      });
      setNewSlug("");
      setNewId("");
      setNewDisplayName("");
      setCreateOpen(false);
    } catch (err) {
      console.error(err);
      // Surface the actual backend error rather than guessing at
      // "ID must be unique and a valid UUID v4". The previous copy
      // misread network failures (transport errors, BFF cold start)
      // as a UUID validation error and confused operators. Connect
      // errors carry rawMessage with the server's typed reason
      // (already-exists, invalid-argument, etc.); plain Error
      // instances fall back to .message; everything else stringifies.
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

  const handleUpdate = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!editing) return;
    try {
      setSubmitting(true);
      await updateTenantMetadata(
        editing.tenantId,
        editing.resourceVersion,
        editDisplayName,
        editing.labels,
      );
      showNotification({
        type: "success",
        title: "Tenant updated",
        message: editDisplayName || editing.tenantId,
      });
      setEditing(null);
    } catch (err) {
      console.error(err);
      showNotification({
        type: "error",
        title: "Update failed",
        message: "Failed to update tenant display name.",
      });
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteTenant(deleteTarget);
      showNotification({
        type: "success",
        title: "Tenant deleted",
        message: deleteTarget,
      });
      setDeleteTarget(null);
    } catch {
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: "The tenant could not be removed.",
      });
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Tenants"
        description="Multi-tenant workspaces and inherited Cedar policy."
        showDefaultActions={false}
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" />
            New tenant
          </Button>
        }
      />

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search tenants by ID or name…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <Button
          variant="outline"
          size="icon"
          onClick={() => {
            void fetchTenants();
          }}
          aria-label="Refresh"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      {/* Table */}
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-[360px]">
                <SortHeader
                  label="Tenant ID"
                  column="tenantId"
                  current={sort}
                  onSort={handleSort}
                />
              </TableHead>
              <TableHead>
                <SortHeader
                  label="Display name"
                  column="displayName"
                  current={sort}
                  onSort={handleSort}
                />
              </TableHead>
              <TableHead className="hidden md:table-cell">Labels</TableHead>
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && tenants.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={4} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className="h-48 text-center">
                  <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <BuildingOfficeIcon className="size-10 opacity-40" />
                    <p className="text-sm">
                      {search
                        ? "No tenants match your search."
                        : "No tenants yet."}
                    </p>
                    {!search && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setCreateOpen(true)}
                      >
                        <PlusIcon className="size-4" />
                        Create the first tenant
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((tenant) => {
                const labelEntries = Object.entries(tenant.labels);
                // Prefer slug for the URL — Tenant proto carries it
                // since the slug-field commit. UUID is the safety net
                // when the field is empty (legacy rows pre-backfill);
                // TenantLayout's resolver canonicalises UUID→slug on
                // landing in either case.
                const handle = tenant.slug || tenant.tenantId;
                const detailHref = `/tenants/${encodeURIComponent(handle)}`;
                return (
                  <TableRow key={tenant.tenantId} className="group">
                    <TableCell>
                      <Link
                        href={detailHref}
                        className="flex items-center gap-3 hover:text-primary"
                      >
                        <div className="flex size-8 items-center justify-center rounded-md bg-primary/15 text-primary ring-1 ring-primary/30">
                          <BuildingOfficeIcon className="size-4" />
                        </div>
                        <span className="font-mono text-xs text-muted-foreground group-hover:text-foreground">
                          {tenant.tenantId}
                        </span>
                      </Link>
                    </TableCell>
                    <TableCell className="font-medium">
                      <Link
                        href={detailHref}
                        className="hover:text-primary hover:underline"
                      >
                        {tenant.displayName || (
                          <span className="text-muted-foreground italic">
                            (unnamed)
                          </span>
                        )}
                      </Link>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      {labelEntries.length === 0 ? (
                        <span className="text-xs text-muted-foreground">—</span>
                      ) : (
                        <div className="flex flex-wrap gap-1">
                          {labelEntries.slice(0, 3).map(([k, v]) => (
                            <Badge
                              key={k}
                              variant="outline"
                              className={cn(T.labelTight, "font-mono")}
                            >
                              {k}={String(v)}
                            </Badge>
                          ))}
                          {labelEntries.length > 3 && (
                            <Badge variant="secondary" className={T.labelTight}>
                              +{labelEntries.length - 3}
                            </Badge>
                          )}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-8 opacity-60 group-hover:opacity-100"
                            aria-label={`Actions for ${tenant.tenantId}`}
                          >
                            <EllipsisHorizontalIcon className="size-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem asChild>
                            <Link href={detailHref}>
                              <ArrowRightIcon className="size-4" />
                              Open tenant
                            </Link>
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onSelect={() => {
                              setEditing(tenant);
                              setEditDisplayName(tenant.displayName || "");
                            }}
                          >
                            <PencilSquareIcon className="size-4" />
                            Edit display name
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            variant="destructive"
                            onSelect={() => setDeleteTarget(tenant.tenantId)}
                          >
                            <TrashIcon className="size-4" />
                            Delete tenant
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      {/* ─── Create dialog ───────────────────────────────────────────────── */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <form onSubmit={handleCreate}>
            <DialogHeader>
              <DialogTitle>New tenant</DialogTitle>
              <DialogDescription>
                Slug is the human-readable handle and is immutable after
                creation. The default Cedar policy is applied automatically.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-4">
              <div className="space-y-1.5">
                <Label htmlFor="tenant-slug">
                  Slug <span className="text-destructive">*</span>
                </Label>
                <Input
                  id="tenant-slug"
                  autoFocus
                  placeholder="acme-prod"
                  value={newSlug}
                  onChange={(e) => setNewSlug(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  3–63 chars, kebab-case. Immutable after create — used in
                  resource names and URLs.
                </p>
              </div>
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <Label htmlFor="tenant-id">
                    Tenant ID{" "}
                    <span className="text-muted-foreground font-normal">
                      (optional)
                    </span>
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
                  placeholder="Leave empty to auto-generate"
                  className="font-mono text-xs"
                  value={newId}
                  onChange={(e) => setNewId(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  UUID — immutable. Server generates UUIDv7 when empty.
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
                  placeholder="Defaults to slug"
                  value={newDisplayName}
                  onChange={(e) => setNewDisplayName(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Unique across tenants. Editable later.
                </p>
              </div>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setCreateOpen(false)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={submitting || !newSlug}>
                {submitting ? "Creating…" : "Create tenant"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* ─── Edit dialog ─────────────────────────────────────────────────── */}
      <Dialog open={!!editing} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent>
          <form onSubmit={handleUpdate}>
            <DialogHeader>
              <DialogTitle>Edit tenant</DialogTitle>
              <DialogDescription>
                Display name is the only editable identity field. Tenant ID and
                slug are immutable — slug rotation requires the RenameTenantSlug
                RPC.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-4">
              <div className="space-y-1.5">
                <Label>Tenant ID</Label>
                <Input
                  disabled
                  value={editing?.tenantId || ""}
                  className="font-mono text-xs text-muted-foreground"
                />
              </div>
              <div className="space-y-1.5">
                <Label>Slug</Label>
                <Input
                  disabled
                  value={editing?.slug || ""}
                  className="text-muted-foreground"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="edit-display-name">Display name</Label>
                <Input
                  id="edit-display-name"
                  autoFocus
                  placeholder="Acme Corporation"
                  value={editDisplayName}
                  onChange={(e) => setEditDisplayName(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Must be unique across tenants.
                </p>
              </div>
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setEditing(null)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={submitting}>
                {submitting ? "Saving…" : "Save changes"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* ─── Delete confirmation ─────────────────────────────────────────── */}
      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(o) => !o && setDeleteTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this tenant?</AlertDialogTitle>
            <AlertDialogDescription>
              All data scoped to{" "}
              <span className="font-mono text-foreground">{deleteTarget}</span>{" "}
              will become inaccessible. This action cannot be undone from the UI
              — recovery requires direct database intervention.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDelete}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Delete tenant
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
