"use client";

import React, { useEffect, useMemo, useState } from "react";
import { useTableSort } from "@/hooks/useTableSort";
import Link from "next/link";
import {
  ArrowPathIcon,
  ArrowRightIcon,
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
import { TenantCreateDialog } from "./TenantCreateDialog";
import { TenantEditDialog } from "./TenantEditDialog";
import { TenantDeleteDialog } from "./TenantDeleteDialog";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { SortableHead } from "@/components/ui/SortHeader";

type SortColumn = "slug" | "displayName";

// Tenant.default_bucket is the resource name of the single (backend, bucket)
// the tenant is bound to: "storageBackends/{backend}/buckets/{bucket}". Empty
// when unset. Split into its parts so the Storage column can render a compact
// "{backend}/{bucket}".
function parseDefaultBucket(
  name: string,
): { backend: string; bucket: string } | null {
  const m = /^storageBackends\/([^/]+)\/buckets\/(.+)$/.exec(name);
  return m ? { backend: m[1], bucket: m[2] } : null;
}

export default function TenantsPage() {
  const {
    tenants,
    loading,
    error: listError,
    fetchTenants,
    createTenant,
    updateTenantMetadata,
    deleteTenant,
  } = useTenants();

  // ─── search + sort ────────────────────────────────────────────────────────
  const [search, setSearch] = useState("");
  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>();

  // Dialog open/target state. The create/edit/delete form state + handlers
  // live inside their respective Tenant*Dialog components; the page only holds
  // which one is open (set by the row menu / New tenant button).
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<Tenant | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Tenant | null>(null);

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
        <div className="relative flex-1 min-w-60">
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
              <SortableHead
                className="@md:w-55"
                label="Slug"
                column="slug"
                current={sort}
                onSort={handleSort}
              />
              <SortableHead
                label="Display name"
                column="displayName"
                current={sort}
                onSort={handleSort}
              />
              <TableHead className="hidden @4xl:table-cell">
                Storage (backend/bucket)
              </TableHead>
              <TableHead className="hidden @md:table-cell">Labels</TableHead>
              <TableHead className="hidden @5xl:table-cell w-70">
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
                  <TableCell colSpan={6} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : listError ? (
              <TableRow>
                <TableCell colSpan={6} className="h-48 text-center">
                  <ListLoadError
                    what="Tenants"
                    reason={listError}
                    onRetry={() => void fetchTenants()}
                  />
                </TableCell>
              </TableRow>
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="h-48 text-center">
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
                const db = parseDefaultBucket(tenant.defaultBucket);
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
                        <span className="font-medium break-all whitespace-normal group-hover:underline">
                          {tenant.slug || (
                            <span className="text-muted-foreground italic">
                              (no slug)
                            </span>
                          )}
                        </span>
                      </Link>
                    </TableCell>
                    {/* Free text: wraps rather than widening the table. */}
                    <TableCell className="whitespace-normal break-words">
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
                    <TableCell className="hidden @4xl:table-cell">
                      {db ? (
                        <span className="font-mono text-xs">
                          {db.backend}/{db.bucket}
                        </span>
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="hidden @md:table-cell">
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
                    <TableCell className="hidden @5xl:table-cell">
                      <span
                        className="font-mono text-sm text-muted-foreground"
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
                          <DropdownMenuItem onSelect={() => setEditing(tenant)}>
                            <PencilSquareIcon className="size-4" />
                            Edit display name
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            variant="destructive"
                            onSelect={() => setDeleteTarget(tenant)}
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
      <TenantEditDialog
        editing={editing}
        onClose={() => setEditing(null)}
        updateTenantMetadata={updateTenantMetadata}
      />

      {/* ─── Delete confirmation ─────────────────────────────────────────── */}
      <TenantDeleteDialog
        deleteTarget={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        deleteTenant={deleteTenant}
      />
    </div>
  );
}
