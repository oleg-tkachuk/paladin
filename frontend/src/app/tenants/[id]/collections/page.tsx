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
import { useTableSort } from "@/hooks/useTableSort";
import Link from "next/link";
import { ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  EllipsisHorizontalIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  ServerStackIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { useCollections } from "@/hooks/useCollections";
import { useBackends } from "@/hooks/useBackends";
import { useAuth } from "@/context/AuthContext";
import { useNotification } from "@/components/ui/Notification";

import { collectionClient } from "@/lib/connect/client";
import { searchFilter } from "@/lib/cel";
import { API_PAGE_SIZE_MAX } from "@/constants";

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
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { isAbortError } from "@/hooks/errorContract";

import { useTenant } from "../tenant-context";
import { failedRead } from "@/components/ui/ListLoadError";
import { CollectionCreateDialog } from "@/components/features/collections/CollectionCreateDialog";
import { SortHeader } from "@/components/ui/SortHeader";

type SortColumn = "name" | "displayName" | "bucket";

export default function TenantCollectionsPage() {
  const tenant = useTenant();
  const { user } = useAuth();
  const { createCollection, deleteCollection } = useCollections();
  const {
    backends: backendRows,
    error: backendsError,
    fetchBackends,
  } = useBackends();
  const { showNotification } = useNotification();

  const isOwnTenant = user?.tenantId === tenant.tenantId;
  // Only backends that can actually receive a Collection. A disabled one
  // cannot, and a drained (read-only) one cannot either — offering them puts
  // a trap in the dialog: the operator picks it, no bucket is available, and
  // the form silently cannot be completed. This cluster accumulated 160
  // backends from tests, all but two of them disabled leftovers, and the
  // dialog defaulted to the alphabetically first — which is exactly the
  // no-buckets case.
  const usableBackends = useMemo(
    () => backendRows.filter((b) => b.enabled && !b.readOnly),
    [backendRows],
  );
  const backends = useMemo(
    () => usableBackends.map((b) => b.backendId),
    [usableBackends],
  );

  const [search, setSearch] = useState("");
  // Trails `search` by 300ms; celFilter is in the react-query key, so without
  // this every keystroke was its own list request.
  const [debouncedSearch, setDebouncedSearch] = useState("");

  // The server takes a CEL expression, not a search string. Sending the raw
  // box contents means "e2e/inv" reaches the plane as an expression and fails
  // to compile ("extraneous input"), so the list comes back empty and the
  // search looks broken.
  //
  // Three things changed when this moved onto searchFilter(). It was
  // `startsWith`, so "logs" did not find "app-logs". It was case-sensitive, so
  // "Logs" did not find "app-logs" either. And it looked at the collection
  // name only, never the display name. All three now match what the other list
  // pages do, and the escaping lives in one place instead of being spelled
  // per page.
  const celFilter = useMemo(
    () => searchFilter(debouncedSearch),
    [debouncedSearch],
  );
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]);

  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>();

  const listQuery = useQuery({
    queryKey: ["tenantCollections", tenant.tenantId, celFilter],
    retry: false, // queryFn toasts real failures.
    queryFn: async ({ signal }) => {
      try {
        const res = await collectionClient.listCollections(
          {
            parent: `tenants/${tenant.tenantId}`,
            page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
            filter: celFilter,
          },
          { signal },
        );
        return res.collections;
      } catch (err) {
        // An aborted query is not a failure the operator needs to see:
        // TanStack cancels in-flight reads on unmount and on supersede.
        if (isAbortError(err)) throw err;
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

  // ── delete confirm
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

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
            placeholder="Search by name or display label…"
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
      <CollectionCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        backends={backends}
        backendsFailed={failedRead(backendsError, fetchBackends)}
        bucketsHref={`/tenants/${tenant.slug}/buckets`}
        createCollection={createCollection}
        onCreated={() => void fetchList(search)}
      />

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
