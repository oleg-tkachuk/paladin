"use client";

// /tenants/<id>/collections — tenant-scoped Collection list.
//
// We call ListCollections directly with `parent = tenants/<tenantId>`
// rather than going through the useCollections hook — the hook
// pins parent to `user.tenantId` from the auth context, which
// would silently return the signed-in user's list for any URL
// (broken when a platform-admin pivots into another tenant).
//
// Create / Delete still go through the hook because they only
// fire for the signed-in tenant in practice — UI hides them
// otherwise. Tracked in BACKLOG: thread tenantId through
// useCollections when platform-admin cross-tenant mutation lands.

import React, { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTableSort, type SortState } from "@/hooks/useTableSort";
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

import { useCollections } from "@/hooks/useCollections";
import { useBuckets } from "@/hooks/useBuckets";
import { useBackends } from "@/hooks/useBackends";
import { useAuth } from "@/context/AuthContext";
import { useNotification } from "@/components/ui/Notification";

import { collectionClient } from "@/lib/connect/client";
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

function SortHeader({
  label,
  column,
  current,
  onSort,
}: {
  label: string;
  column: SortColumn;
  current: SortState<SortColumn>;
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

export default function TenantCollectionsPage() {
  const tenant = useTenant();
  const { user } = useAuth();
  const { createCollection, deleteCollection } = useCollections();
  const { buckets, fetchBuckets } = useBuckets();
  const { backends: backendRows } = useBackends();
  const { showNotification } = useNotification();

  const isOwnTenant = user?.tenantId === tenant.tenantId;
  const backends = useMemo(
    () => backendRows.map((b) => b.backendId),
    [backendRows],
  );

  const [search, setSearch] = useState("");
  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>();

  const listQuery = useQuery({
    queryKey: ["tenantCollections", tenant.tenantId, search],
    retry: false, // queryFn toasts real failures.
    queryFn: async ({ signal }) => {
      try {
        const res = await collectionClient.listCollections(
          {
            parent: `tenants/${tenant.tenantId}`,
            page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
            filter: search,
          },
          { signal },
        );
        return res.collections;
      } catch (err) {
        showNotification({
          type: "error",
          title: "Load failed",
          message:
            err instanceof ConnectError
              ? err.rawMessage
              : "Failed to fetch collections",
        });
        throw err;
      }
    },
  });
  const list = useMemo(() => listQuery.data ?? [], [listQuery.data]);
  const loading = listQuery.isFetching;
  // Call sites pass the current search; it's already in the queryKey, so a
  // bare refetch suffices.
  const fetchList = (_filter?: string) => listQuery.refetch();

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

  // Default the create form to the first backend / first bucket once the
  // dialog opens and the lists have loaded. Render-phase adjust-on-condition
  // (the !newBackend / !newBucketRef guards converge in one extra render) —
  // not set-state-in-effect.
  if (createOpen && !newBackend && backends.length > 0) {
    setNewBackend(backends[0]);
  }

  const availableBuckets = useMemo(
    () =>
      newBackend ? buckets.filter((b) => b.backendId === newBackend) : buckets,
    [buckets, newBackend],
  );

  if (createOpen && !newBucketRef && availableBuckets.length > 0) {
    setNewBucketRef(availableBuckets[0].bucketId);
  }

  const sorted = useMemo(() => {
    const arr = [...list];
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      arr.sort((a, b) => {
        const va =
          sort.column === "name"
            ? a.collection
            : sort.column === "displayName"
              ? a.displayName || ""
              : a.bucket || "";
        const vb =
          sort.column === "name"
            ? b.collection
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
      await createCollection(newName, newDisplayName, newBackend, newBucketRef);
      showNotification({
        type: "success",
        title: "Collection created",
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
        message: (err as Error).message || "Failed to create Collection.",
      });
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      // OCC guard: DeleteCollection requires the version we last read, so a
      // concurrent rename/update turns this into a 409 instead of a silent
      // delete of something the user never saw.
      const target = list.find((c) => c.collection === deleteTarget);
      await deleteCollection(deleteTarget, target?.resourceVersion ?? "");
      showNotification({
        type: "success",
        title: "Collection deleted",
        message: deleteTarget,
      });
      setDeleteTarget(null);
      void fetchList(search);
    } catch {
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: "Collection must be empty before it can be removed.",
      });
    }
  };

  const detailHref = (collection: string) =>
    `/tenants/${tenant.slug}/collections/${encodeURIComponent(collection)}`;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Collections</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Tenant-scoped namespaces routed to a physical bucket. Cross-tenant
            index lives at{" "}
            <Link
              href="/collections"
              className="text-primary hover:underline font-mono"
            >
              /collections
            </Link>
            .
          </p>
        </div>
        {isOwnTenant && (
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" />
            New Collection
          </Button>
        )}
      </div>

      {/* Filter bar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search Collections by prefix…"
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
                  label="Collection"
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
                        ? "No Collections match your search."
                        : "No Collections yet."}
                    </p>
                    {!search && isOwnTenant && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setCreateOpen(true)}
                      >
                        <PlusIcon className="size-4" />
                        Provision the first Collection
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              sorted.map((ok) => (
                <TableRow key={ok.collection} className="group">
                  <TableCell>
                    <div className="flex items-center gap-3">
                      <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                        <ServerStackIcon className="size-4" />
                      </div>
                      <Link
                        href={detailHref(ok.collection)}
                        className="font-mono text-xs hover:text-primary hover:underline"
                        title={ok.collection}
                      >
                        {ok.collection}
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
                          aria-label={`Actions for ${ok.collection}`}
                        >
                          <EllipsisHorizontalIcon className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem asChild>
                          <Link href={detailHref(ok.collection)}>
                            View details
                          </Link>
                        </DropdownMenuItem>
                        {isOwnTenant && (
                          <DropdownMenuItem
                            variant="destructive"
                            onSelect={() => setDeleteTarget(ok.collection)}
                          >
                            <TrashIcon className="size-4" />
                            Delete Collection
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
                Provision Collection for{" "}
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
                        key={`${b.backendId}/${b.bucketId}`}
                        value={b.bucketId}
                      >
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
                <Label htmlFor="ok-name">Collection path</Label>
                <Input
                  id="ok-name"
                  autoFocus
                  placeholder="invoices/2026/q1"
                  className="font-mono text-xs"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value.toLowerCase())}
                />
                <p className={T.hint}>
                  Slash-separated path. Each segment: 1–63 lowercase
                  alphanumerics or hyphens (kebab-case), starts and ends
                  alphanumeric. Examples:{" "}
                  <span className={T.code}>assets-prod</span> or{" "}
                  <span className={T.code}>invoices/2026/q1</span>.
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
                {submitting ? "Provisioning…" : "Create Collection"}
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
            <AlertDialogTitle>Delete this Collection?</AlertDialogTitle>
            <AlertDialogDescription>
              Removing{" "}
              <span className="font-mono text-foreground">{deleteTarget}</span>.
              The Collection must be empty of all live objects before this can
              succeed.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDelete}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Delete Collection
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
