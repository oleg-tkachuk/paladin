"use client";

// /tenants/<id>/buckets — tenant-scoped bucket list.
//
// Filtering strategy: pushes `owner_tenant_id == tenantId` to the
// server via ListBucketsRequest.owner_tenant_id (backed by the
// partial index on buckets.owner_tenant_id from migration 006).
// The cross-tenant /buckets page omits the filter; the same
// useBuckets hook serves both.
//
// Rows link to the bucket detail subtree under the same tenant. The
// "Create bucket" affordance pre-fills owner_tenant_id with the
// current tenant — operators can still un-set it via the cross-tenant
// page if they want a shared bucket.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useTableSort } from "@/hooks/useTableSort";
import Link from "next/link";

import { errorMessage } from "@/hooks/errorContract";
import {
  ArchiveBoxIcon,
  ArrowPathIcon,
  ClockIcon,
  EllipsisHorizontalIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  ServerStackIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { useBuckets } from "@/hooks/useBuckets";
import { useBackends } from "@/hooks/useBackends";
import { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { useNotification } from "@/components/ui/Notification";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/Card";
import { Checkbox } from "@/components/ui/checkbox";
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
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
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

import { useTenant } from "../tenant-context";
import { failedRead } from "@/components/ui/ListLoadError";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { BucketCreateDialog } from "@/components/features/buckets/BucketCreateDialog";
import { isProvisionInFlight, PROVISION_POLL_MS } from "@/lib/bucketProvision";
import { useRefetchWhile } from "@/hooks/useRefetchWhile";
import { SortableHead } from "@/components/ui/SortHeader";

type SortColumn = "backend" | "name" | "region";

const ALL_BACKENDS = "__all__";

export default function TenantBucketsPage() {
  const tenant = useTenant();
  const {
    buckets,
    loading,
    error: bucketsError,
    fetchBuckets,
    createBucket,
    deleteBucket,
  } = useBuckets();
  const {
    backends: backendRows,
    error: backendsError,
    fetchBackends,
  } = useBackends();
  const { showNotification } = useNotification();

  const backends = useMemo(
    () => backendRows.map((b) => b.backendId),
    [backendRows],
  );

  const [search, setSearch] = useState("");
  const [filterBackend, setFilterBackend] = useState<string>(ALL_BACKENDS);
  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>();

  const [createOpen, setCreateOpen] = useState(false);

  const [deleteTarget, setDeleteTarget] = useState<Bucket | null>(null);
  const [deleteRemote, setDeleteRemote] = useState(false);

  // Push the tenant filter to the server. Hook signature is
  // (backendId?, filter?, pageToken?, ownerTenantId?) — leave
  // backend/filter/cursor empty to list all buckets owned by
  // this tenant across backends. One place, so Refresh asks for the same.
  const refetch = useCallback(
    () => fetchBuckets(undefined, "", "", tenant.tenantId),
    [fetchBuckets, tenant.tenantId],
  );

  useEffect(() => {
    void refetch();
  }, [refetch]);

  // A created or deleted bucket settles in the reconciler, not in the RPC
  // that started it; without this it read "provisioning…" until a reload.
  useRefetchWhile(
    buckets.some((b) => isProvisionInFlight(b.provisionState)),
    () => void refetch(),
    PROVISION_POLL_MS,
  );

  // The server-side `owner_tenant_id` filter (passed in fetchBuckets
  // above) already narrows `buckets` to this tenant's rows; we list
  // them directly. Cross-tenant /buckets uses the same hook without
  // the filter.

  const filtered = useMemo(() => {
    let list = buckets;
    if (filterBackend !== ALL_BACKENDS) {
      list = list.filter((b) => b.backendId === filterBackend);
    }
    const q = search.trim().toLowerCase();
    if (q) {
      list = list.filter(
        (b) =>
          b.bucketId.toLowerCase().includes(q) ||
          (b.displayName || "").toLowerCase().includes(q),
      );
    }
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      list = [...list].sort((a, b) => {
        const va =
          sort.column === "backend"
            ? a.backendId
            : sort.column === "name"
              ? a.bucketId
              : a.region || "";
        const vb =
          sort.column === "backend"
            ? b.backendId
            : sort.column === "name"
              ? b.bucketId
              : b.region || "";
        return va < vb ? -dir : va > vb ? dir : 0;
      });
    }
    return list;
  }, [buckets, filterBackend, search, sort]);

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteBucket(
        deleteTarget.backendId,
        deleteTarget.bucketId,
        deleteTarget.resourceVersion,
        deleteRemote,
      );
      showNotification({
        type: "success",
        title: "Bucket deleted",
        message: `${deleteTarget.bucketId}${deleteRemote ? " (incl. S3)" : ""}`,
      });
      setDeleteTarget(null);
      setDeleteRemote(false);
    } catch (err) {
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: errorMessage(err, "Failed to delete bucket."),
      });
    }
  };

  const detailHref = (b: Bucket) =>
    `/tenants/${tenant.slug}/buckets/${encodeURIComponent(
      b.backendId,
    )}/${encodeURIComponent(b.bucketId)}`;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Buckets</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            S3 buckets owned by this tenant. Cross-tenant index lives at{" "}
            <Link
              href="/buckets"
              className="text-primary hover:underline font-mono"
            >
              /buckets
            </Link>
            .
          </p>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <PlusIcon className="size-4" />
          New bucket
        </Button>
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search by name or display label…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <SelectRoot value={filterBackend} onValueChange={setFilterBackend}>
          <SelectTrigger aria-label="Filter by backend" className="w-[200px]">
            <SelectValue placeholder="All backends" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_BACKENDS}>All backends</SelectItem>
            {backends.map((b) => (
              <SelectItem key={b} value={b}>
                {b}
              </SelectItem>
            ))}
          </SelectContent>
        </SelectRoot>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void refetch()}
          aria-label="Refresh"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <SortableHead
                className="w-[180px]"
                label="Backend"
                column="backend"
                current={sort}
                onSort={handleSort}
              />
              <SortableHead
                label="Bucket name"
                column="name"
                current={sort}
                onSort={handleSort}
              />
              <TableHead className="hidden sm:table-cell">
                Display name
              </TableHead>
              <SortableHead
                className="hidden md:table-cell"
                label="Region"
                column="region"
                current={sort}
                onSort={handleSort}
              />
              <TableHead className="w-[140px]">Status</TableHead>
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && buckets.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={6} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : bucketsError ? (
              <TableRow>
                <TableCell colSpan={6} className="h-48 text-center">
                  <ListLoadError
                    what="Buckets"
                    reason={bucketsError}
                    onRetry={() => void refetch()}
                  />
                </TableCell>
              </TableRow>
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="h-48 text-center">
                  <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <ArchiveBoxIcon className="size-10 opacity-40" />
                    <p className="text-sm">
                      {search || filterBackend !== ALL_BACKENDS
                        ? "No buckets match your filters."
                        : "No buckets owned by this tenant yet."}
                    </p>
                    {!search && filterBackend === ALL_BACKENDS && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setCreateOpen(true)}
                      >
                        <PlusIcon className="size-4" />
                        Create the first bucket
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((b) => (
                <TableRow
                  key={`${b.backendId}/${b.bucketId}`}
                  className="group"
                >
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <ServerStackIcon className="size-4 text-chart-5" />
                      <Badge variant="info" className={T.code}>
                        {b.backendId}
                      </Badge>
                    </div>
                  </TableCell>
                  <TableCell>
                    <Link
                      href={detailHref(b)}
                      className="flex items-center gap-3 hover:text-primary"
                    >
                      <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                        <ArchiveBoxIcon className="size-4" />
                      </div>
                      <span className="font-mono text-xs">{b.bucketId}</span>
                    </Link>
                  </TableCell>
                  <TableCell className="hidden sm:table-cell font-medium">
                    {b.displayName || (
                      <span className="text-muted-foreground italic">—</span>
                    )}
                  </TableCell>
                  <TableCell className="hidden md:table-cell text-muted-foreground text-xs font-mono">
                    {b.region || "—"}
                  </TableCell>
                  <TableCell>
                    <ProvisionStateBadge state={b.provisionState} />
                  </TableCell>
                  <TableCell className="text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8 opacity-60 group-hover:opacity-100"
                          aria-label={`Actions for ${b.bucketId}`}
                        >
                          <EllipsisHorizontalIcon className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem asChild>
                          <Link href={`${detailHref(b)}/lifecycle`}>
                            <ClockIcon className="size-4" />
                            Lifecycle rules
                          </Link>
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          variant="destructive"
                          onSelect={() => setDeleteTarget(b)}
                        >
                          <TrashIcon className="size-4" />
                          Delete bucket
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      <BucketCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        backends={backends}
        backendsFailed={failedRead(backendsError, fetchBackends)}
        createBucket={createBucket}
      />

      {/* ─── Delete confirmation ─────────────────────────────────────────── */}
      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(o) => {
          if (!o) {
            setDeleteTarget(null);
            setDeleteRemote(false);
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this bucket?</AlertDialogTitle>
            <AlertDialogDescription>
              Removing{" "}
              <span className="font-mono text-foreground">
                {deleteTarget?.bucketId}
              </span>{" "}
              from backend{" "}
              <span className="font-mono text-foreground">
                {deleteTarget?.backendId}
              </span>
              . Any Collection still bound to it must be removed first.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <label className="flex cursor-pointer items-start gap-3 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm">
            <Checkbox
              checked={deleteRemote}
              onCheckedChange={(v) => setDeleteRemote(v === true)}
              id="delete-remote"
              className="mt-0.5"
            />
            <div>
              <Label
                htmlFor="delete-remote"
                className="cursor-pointer text-destructive"
              >
                Also delete the physical S3 bucket
              </Label>
              <p className="mt-0.5 text-xs text-muted-foreground">
                The S3 bucket must already be empty for this to succeed.
              </p>
            </div>
          </label>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDelete}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Delete bucket
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function ProvisionStateBadge({ state }: { state: string }) {
  const s = state || "ready";
  switch (s) {
    case "ready":
      return (
        <Badge
          variant="outline"
          className={cn(T.code, "text-muted-foreground")}
        >
          ready
        </Badge>
      );
    case "pending":
      return (
        <Badge variant="info" className={T.code}>
          provisioning…
        </Badge>
      );
    case "deleting":
      return (
        <Badge variant="warning" className={T.code}>
          deleting…
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive" className={T.code}>
          failed
        </Badge>
      );
    case "deletion_failed":
      return (
        <Badge variant="destructive" className={T.code}>
          delete failed
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className={T.code}>
          {s}
        </Badge>
      );
  }
}
