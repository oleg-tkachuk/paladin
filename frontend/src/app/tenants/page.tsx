"use client";

import React, { useEffect, useMemo, useState } from "react";
import { useTableSort, type SortState } from "@/hooks/useTableSort";
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
  TrashIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useTenants } from "@/hooks/useTenants";
import { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { useNotification } from "@/components/ui/Notification";
import { TenantCreateDialog } from "./TenantCreateDialog";

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
import { IdentityField } from "@/components/IdentityField";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

type SortColumn = "slug" | "displayName";

function SortHeader({
  label,
  column,
  current,
  onSort,
  className,
}: {
  label: string;
  column: SortColumn;
  current: SortState<SortColumn>;
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
  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>();

  // ─── create ───────────────────────────────────────────────────────────────
  // Phase 0 contract: slug is required, tenant_id is optional (server
  // mints UUIDv7 when empty), display_name is optional (defaults to
  // slug). Both slug and display_name are unique across tenants.
  // Create-dialog open state; all the form state + backend/bucket cascade now
  // lives inside TenantCreateDialog.
  const [createOpen, setCreateOpen] = useState(false);
  // Shared by the edit flow below (create has its own submitting state now).
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
      // Search across all three identity fields. Slug is the most
      // common keystroke target, ID is the audit/debug fallback.
      list = list.filter(
        (t) =>
          (t.slug || "").toLowerCase().includes(q) ||
          (t.displayName || "").toLowerCase().includes(q) ||
          t.tenantId.toLowerCase().includes(q),
      );
    }
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      list = [...list].sort((a, b) => {
        const va =
          sort.column === "slug"
            ? (a.slug || a.tenantId).toLowerCase()
            : (a.displayName || a.slug || a.tenantId).toLowerCase();
        const vb =
          sort.column === "slug"
            ? (b.slug || b.tenantId).toLowerCase()
            : (b.displayName || b.slug || b.tenantId).toLowerCase();
        return va < vb ? -dir : va > vb ? dir : 0;
      });
    }
    return list;
  }, [tenants, search, sort]);

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
              <TableHead className="w-[220px]">
                <SortHeader
                  label="Slug"
                  column="slug"
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
              <TableHead className="hidden lg:table-cell w-[280px]">
                Tenant ID
              </TableHead>
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && tenants.length === 0 ? (
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
                // Slug is the canonical handle in URLs; UUID is the
                // safety net when slug is empty (legacy rows). Phase 0
                // makes slug NOT NULL UNIQUE, so the fallback only
                // matters during the rolling deploy.
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
                        <span className="font-medium group-hover:underline">
                          {tenant.slug || (
                            <span className="text-muted-foreground italic">
                              (no slug)
                            </span>
                          )}
                        </span>
                      </Link>
                    </TableCell>
                    <TableCell>
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
                    <TableCell className="hidden lg:table-cell">
                      <span
                        className="font-mono text-[11px] text-muted-foreground"
                        title={tenant.tenantId}
                      >
                        {tenant.tenantId.slice(0, 8)}…
                        {tenant.tenantId.slice(-4)}
                      </span>
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
      <TenantCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        createTenant={createTenant}
      />

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
              {/* Immutable identity rows — IdentityField gives the
                  operator a copy affordance, matching the overview
                  page. Plain disabled <Input> didn't. */}
              <div className="space-y-2 rounded-md border border-border bg-muted/30 px-3 py-2">
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
            <AlertDialogDescription asChild>
              <div className="space-y-3">
                <p>
                  All data scoped to{" "}
                  <span className="font-mono text-foreground">
                    {deleteTarget}
                  </span>{" "}
                  will become inaccessible.
                </p>
                <div className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs space-y-1">
                  <p className="font-semibold text-destructive">
                    What gets removed (cascade)
                  </p>
                  <ul className="list-disc pl-5 text-muted-foreground space-y-0.5">
                    <li>Default backend/bucket binding</li>
                    <li>All Object Keys + their cedar policies</li>
                    <li>Audit log entries (after retention TTL)</li>
                    <li>API tokens, M2M tokens, capabilities</li>
                  </ul>
                </div>
                <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs space-y-1">
                  <p className="font-semibold">What stays</p>
                  <ul className="list-disc pl-5 text-muted-foreground space-y-0.5">
                    <li>
                      Physical S3 objects under{" "}
                      <span className={T.code}>{"<bucket>/<tenant_id>/…"}</span>
                    </li>
                    <li>
                      In-flight presigned URLs (continue working until TTL
                      expires)
                    </li>
                  </ul>
                </div>
                <p className="text-xs italic text-muted-foreground">
                  Recovery requires direct database intervention.
                </p>
              </div>
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
