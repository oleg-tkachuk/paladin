"use client";

// /tenants/<id>/object-keys — tenant-scoped ObjectKey list.
//
// We call ListObjectKeys directly with `parent = tenants/<tenantId>`
// rather than going through the useObjectKeys hook — the hook
// pins parent to `user.tenantId` from the auth context, which
// would silently return the signed-in user's list for any URL
// (broken when a platform-admin pivots into another tenant).
//
// Create / Delete still go through the hook because they only
// fire for the signed-in tenant in practice — UI hides them
// otherwise. Tracked in BACKLOG: thread tenantId through
// useObjectKeys when platform-admin cross-tenant mutation lands.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  ArrowsUpDownIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  EllipsisHorizontalIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  ServerStackIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { useObjectKeys } from "@/hooks/useObjectKeys";
import { useBuckets } from "@/hooks/useBuckets";
import { useBackends } from "@/hooks/useBackends";
import { useAuth } from "@/context/AuthContext";
import { useNotification } from "@/components/ui/Notification";

import { objectKeyClient } from "@/lib/connect/client";
import type { ObjectKey } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";

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

type SortColumn = "name" | "displayName" | "bucket";
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
}: {
  label: string;
  column: SortColumn;
  current: SortState;
  onSort: (c: SortColumn) => void;
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
      )}
    >
      {label}
      <Icon className="size-3.5 opacity-70" />
    </button>
  );
}

export default function TenantObjectKeysPage() {
  const tenant = useTenant();
  const { user } = useAuth();
  const { createObjectKey, deleteObjectKey } = useObjectKeys();
  const { buckets, fetchBuckets } = useBuckets();
  const { backends: backendRows } = useBackends();
  const { showNotification } = useNotification();

  const isOwnTenant = user?.tenantId === tenant.tenantId;
  const backends = useMemo(
    () => backendRows.map((b) => b.backendId),
    [backendRows],
  );

  const [list, setList] = useState<ObjectKey[]>([]);
  const [loading, setLoading] = useState(false);
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<SortState>({
    column: null,
    direction: null,
  });
  const handleSort = useCallback(
    (c: SortColumn) => setSort((prev) => nextSort(prev, c)),
    [],
  );

  const fetchList = useCallback(
    async (filter: string = "") => {
      setLoading(true);
      try {
        const res = await objectKeyClient.listObjectKeys({
          parent: `tenants/${tenant.tenantId}`,
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
          filter,
        });
        setList(res.objectKeys);
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to fetch object keys";
        showNotification({ type: "error", title: "Load failed", message: msg });
      } finally {
        setLoading(false);
      }
    },
    [tenant.tenantId, showNotification],
  );

  useEffect(() => {
    void fetchList(search);
  }, [fetchList, search]);

  // ── create dialog
  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [newDisplayName, setNewDisplayName] = useState("");
  const [newBackend, setNewBackend] = useState("");
  const [newBucketRef, setNewBucketRef] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // ── delete confirm
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  useEffect(() => {
    if (createOpen) fetchBuckets(newBackend || undefined);
  }, [createOpen, newBackend, fetchBuckets]);

  useEffect(() => {
    if (createOpen && !newBackend && backends.length > 0) {
      setNewBackend(backends[0]);
    }
  }, [createOpen, newBackend, backends]);

  const availableBuckets = useMemo(
    () =>
      newBackend ? buckets.filter((b) => b.backendId === newBackend) : buckets,
    [buckets, newBackend],
  );

  useEffect(() => {
    if (createOpen && !newBucketRef && availableBuckets.length > 0) {
      setNewBucketRef(availableBuckets[0].bucketName);
    }
  }, [createOpen, newBucketRef, availableBuckets]);

  const sorted = useMemo(() => {
    const arr = [...list];
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      arr.sort((a, b) => {
        const va =
          sort.column === "name"
            ? a.objectKey
            : sort.column === "displayName"
              ? a.displayName || ""
              : a.bucket || "";
        const vb =
          sort.column === "name"
            ? b.objectKey
            : sort.column === "displayName"
              ? b.displayName || ""
              : b.bucket || "";
        return va < vb ? -dir : va > vb ? dir : 0;
      });
    }
    return arr;
  }, [list, sort]);

  const handleCreate = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (!newName || !newBucketRef) return;
    try {
      setSubmitting(true);
      await createObjectKey(newName, newDisplayName, newBackend, newBucketRef);
      showNotification({
        type: "success",
        title: "ObjectKey created",
        message: `${newName} → s3://${newBucketRef}/`,
      });
      setNewName("");
      setNewDisplayName("");
      setCreateOpen(false);
      void fetchList(search);
    } catch (err) {
      showNotification({
        type: "error",
        title: "Creation failed",
        message: (err as Error).message || "Failed to create ObjectKey.",
      });
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      await deleteObjectKey(deleteTarget);
      showNotification({
        type: "success",
        title: "ObjectKey deleted",
        message: deleteTarget,
      });
      setDeleteTarget(null);
      void fetchList(search);
    } catch {
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: "ObjectKey must be empty before it can be removed.",
      });
    }
  };

  const detailHref = (objectKey: string) =>
    `/tenants/${tenant.slug}/object-keys/${encodeURIComponent(objectKey)}`;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Object Keys</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Tenant-scoped namespaces routed to a physical bucket. Cross-tenant
            index lives at{" "}
            <Link
              href="/object-keys"
              className="text-primary hover:underline font-mono"
            >
              /object-keys
            </Link>
            .
          </p>
        </div>
        {isOwnTenant && (
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" />
            New ObjectKey
          </Button>
        )}
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search Object Keys by prefix…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void fetchList(search)}
          aria-label="Refresh"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>
                <SortHeader
                  label="ObjectKey"
                  column="name"
                  current={sort}
                  onSort={handleSort}
                />
              </TableHead>
              <TableHead className="hidden sm:table-cell">
                <SortHeader
                  label="Display name"
                  column="displayName"
                  current={sort}
                  onSort={handleSort}
                />
              </TableHead>
              <TableHead className="hidden md:table-cell">
                <SortHeader
                  label="Bucket"
                  column="bucket"
                  current={sort}
                  onSort={handleSort}
                />
              </TableHead>
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && list.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={4} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : sorted.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className="h-48 text-center">
                  <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <ServerStackIcon className="size-10 opacity-40" />
                    <p className="text-sm">
                      {search
                        ? "No Object Keys match your search."
                        : "No Object Keys yet."}
                    </p>
                    {!search && isOwnTenant && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setCreateOpen(true)}
                      >
                        <PlusIcon className="size-4" />
                        Provision the first ObjectKey
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              sorted.map((ok) => (
                <TableRow key={ok.objectKey} className="group">
                  <TableCell>
                    <div className="flex items-center gap-3">
                      <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                        <ServerStackIcon className="size-4" />
                      </div>
                      <Link
                        href={detailHref(ok.objectKey)}
                        className="font-mono text-xs hover:text-primary hover:underline"
                        title={ok.objectKey}
                      >
                        {ok.objectKey}
                      </Link>
                    </div>
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    {ok.displayName || (
                      <span className="text-muted-foreground italic">—</span>
                    )}
                  </TableCell>
                  <TableCell className="hidden md:table-cell">
                    {ok.bucket ? (
                      <Badge variant="info" className={T.code}>
                        {ok.bucket}
                      </Badge>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8 opacity-60 group-hover:opacity-100"
                          aria-label={`Actions for ${ok.objectKey}`}
                        >
                          <EllipsisHorizontalIcon className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem asChild>
                          <Link href={detailHref(ok.objectKey)}>
                            View details
                          </Link>
                        </DropdownMenuItem>
                        {isOwnTenant && (
                          <DropdownMenuItem
                            variant="destructive"
                            onSelect={() => setDeleteTarget(ok.objectKey)}
                          >
                            <TrashIcon className="size-4" />
                            Delete ObjectKey
                          </DropdownMenuItem>
                        )}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      {/* Create dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <form onSubmit={handleCreate}>
            <DialogHeader>
              <DialogTitle>
                Provision ObjectKey for{" "}
                <span className="font-mono">{tenant.slug}</span>
              </DialogTitle>
              <DialogDescription>
                Tenant-scoped prefix bound to a physical S3 bucket. Layout is{" "}
                <code className="font-mono text-foreground">
                  s3://&lt;bucket&gt;/&lt;tenant&gt;/&lt;object_key&gt;/&lt;key&gt;
                </code>
                .
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-4">
              <div className="space-y-1.5">
                <Label htmlFor="ok-backend">Storage backend</Label>
                <SelectRoot
                  value={newBackend}
                  onValueChange={(v) => {
                    setNewBackend(v);
                    setNewBucketRef("");
                  }}
                  disabled={backends.length === 0}
                >
                  <SelectTrigger id="ok-backend" className="w-full">
                    <SelectValue
                      placeholder={
                        backends.length === 0
                          ? "— no backends configured —"
                          : "Select a backend"
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
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="ok-bucket">S3 bucket</Label>
                <SelectRoot
                  value={newBucketRef}
                  onValueChange={setNewBucketRef}
                  disabled={availableBuckets.length === 0}
                >
                  <SelectTrigger id="ok-bucket" className="w-full">
                    <SelectValue
                      placeholder={
                        availableBuckets.length === 0
                          ? "— no buckets in this backend —"
                          : "Select a bucket"
                      }
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {availableBuckets.map((b) => (
                      <SelectItem
                        key={`${b.backendId}/${b.bucketName}`}
                        value={b.bucketName}
                      >
                        <span className="font-mono">{b.bucketName}</span>
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
                {availableBuckets.length === 0 && newBackend && (
                  <p className="text-xs text-destructive">
                    Create a bucket in this backend via{" "}
                    <Link
                      href={`/tenants/${tenant.slug}/buckets`}
                      className="underline"
                    >
                      the tenant Buckets tab
                    </Link>{" "}
                    first.
                  </p>
                )}
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="ok-name">ObjectKey name</Label>
                <Input
                  id="ok-name"
                  autoFocus
                  placeholder="assets-prod"
                  className="font-mono text-xs"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value.toLowerCase())}
                />
                <p className={T.hint}>
                  3–63 lowercase alphanumerics or hyphens.
                </p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="ok-display-name">Display name</Label>
                <Input
                  id="ok-display-name"
                  placeholder="Production assets"
                  value={newDisplayName}
                  onChange={(e) => setNewDisplayName(e.target.value)}
                />
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
              <Button
                type="submit"
                disabled={submitting || !newName || !newBucketRef}
              >
                {submitting ? "Provisioning…" : "Create ObjectKey"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Delete */}
      <AlertDialog
        open={!!deleteTarget}
        onOpenChange={(o) => !o && setDeleteTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this ObjectKey?</AlertDialogTitle>
            <AlertDialogDescription>
              Removing{" "}
              <span className="font-mono text-foreground">{deleteTarget}</span>.
              The ObjectKey must be empty of all live objects before this can
              succeed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDelete}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Delete ObjectKey
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
