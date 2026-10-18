"use client";

import { CollectionAccess } from "@/gen/paladin/admin/v1/types_pb";
import { PublicReadBadge } from "@/components/features/buckets/PublicReadBadge";
import React, { useEffect, useMemo, useState } from "react";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { errorMessage } from "@/hooks/errorContract";
import { useTableSort } from "@/hooks/useTableSort";
import Link from "next/link";
import {
  ArrowPathIcon,
  EllipsisHorizontalIcon,
  PlusIcon,
  ServerStackIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useCollections } from "@/hooks/useCollections";
import { useTenants } from "@/hooks/useTenants";
import {
  tenantLabel,
  tenantLabelMap,
} from "@/components/features/buckets/BucketTable";
import { collectionHref, tenantHref } from "@/lib/navigation/hrefs";
import { BucketNameLink } from "@/components/features/buckets/BucketNameLink";
import { useBackends } from "@/hooks/useBackends";
import { useNotification } from "@/components/ui/Notification";
import { TableToolbar } from "@/components/ui/TableToolbar";

import { Button } from "@/components/ui/button";
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
import { failedRead, ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { searchFilter } from "@/lib/cel";
import { CollectionCreateDialog } from "@/components/features/collections/CollectionCreateDialog";
import { CollectionDeleteDialog } from "@/components/features/collections/CollectionDeleteDialog";
import { SortableHead } from "@/components/ui/SortHeader";

type SortColumn = "name" | "displayName" | "bucket";

/** Collection, tenant, display name, bucket, actions. */
const COLUMNS = 5;

export default function CollectionsPage() {
  const {
    collections,
    loading,
    error: listError,
    fetchAllCollections,
    createCollection,
    deleteCollection,
  } = useCollections({ catalogue: true });
  // The rows span tenants; each names its own.
  const { tenants, fetchTenants } = useTenants();
  useEffect(() => {
    void fetchTenants();
  }, [fetchTenants]);
  const tenantNames = useMemo(() => tenantLabelMap(tenants), [tenants]);
  const {
    backends: backendRows,
    error: backendsError,
    fetchBackends,
  } = useBackends();
  const { showNotification } = useNotification();

  // Only backends that can take a Collection: a disabled or draining one
  // leaves a dialog that cannot be completed.
  const backends = useMemo(
    () =>
      backendRows
        .filter((b) => b.enabled && !b.readOnly)
        .map((b) => b.backendId),
    [backendRows],
  );

  // ── search + sort
  const [search, setSearch] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>();

  // ── create
  const [createOpen, setCreateOpen] = useState(false);

  // ── delete
  // The row as it was when Delete was chosen. Its resourceVersion is the OCC
  // guard; looking it up again at confirm read whatever the list held then —
  // nothing, if a search had just changed the query — and sent an empty one.
  const [deleteTarget, setDeleteTarget] = useState<Collection | null>(null);

  // The box contents used to go STRAIGHT into `filter`, which the server
  // compiles as CEL. Typing "logs" sent the expression `logs`, an undeclared
  // identifier, so the plane answered InvalidArgument and the list emptied —
  // the search box on this page did not work at all. The neighbouring
  // tenant-scoped page carries a comment describing exactly this failure; it
  // was never fixed here.
  //
  // searchFilter() builds the expression, and the debounce stops a refetch per
  // keystroke — this fired one list request per character before.
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]);

  useEffect(() => {
    void fetchAllCollections(searchFilter(debouncedSearch));
  }, [fetchAllCollections, debouncedSearch]);

  const sorted = useMemo(() => {
    const list = [...collections];
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      list.sort((a, b) => {
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
    return list;
  }, [collections, sort]);

  const handleDelete = async () => {
    if (!deleteTarget) return;
    try {
      // OCC guard: DeleteCollection requires the version we last read, so a
      // concurrent rename/update turns this into a 409 instead of a silent
      // delete of something the user never saw.
      await deleteCollection(
        deleteTarget.collection,
        deleteTarget.resourceVersion,
        false,
        deleteTarget.tenantId,
      );
      showNotification({
        type: "success",
        title: "Collection deleted",
        message: deleteTarget.collection,
      });
      setDeleteTarget(null);
    } catch (err) {
      // The server's reason: a collection that still holds objects is one,
      // a stale version another. This used to say the first whatever happened.
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: errorMessage(err, "Delete failed"),
      });
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Collections"
        description="Every tenant's Collections, each routed to a bucket. A new one is created in your tenant."
        showDefaultActions={false}
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" />
            New Collection
          </Button>
        }
      />

      {/* Filter bar */}
      <TableToolbar
        query={search}
        onQueryChange={setSearch}
        searchLabel="Search collections"
        placeholder="Search by name or display label…"
        actions={
          <>
            <Button
              variant="outline"
              size="icon"
              onClick={() =>
                void fetchAllCollections(searchFilter(debouncedSearch))
              }
              aria-label="Refresh"
            >
              <ArrowPathIcon
                className={cn("size-4", loading && "animate-spin")}
              />
            </Button>
          </>
        }
      />

      {/* Table */}
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <SortableHead
                label="Collection"
                column="name"
                current={sort}
                onSort={handleSort}
              />
              <TableHead>Tenant</TableHead>
              <SortableHead
                className="hidden @xs:table-cell"
                label="Display name"
                column="displayName"
                current={sort}
                onSort={handleSort}
              />
              <SortableHead
                className="hidden @md:table-cell"
                label="Bucket"
                column="bucket"
                current={sort}
                onSort={handleSort}
              />
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && collections.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={COLUMNS} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : listError ? (
              <TableRow>
                <TableCell colSpan={COLUMNS} className="h-48 text-center">
                  <ListLoadError
                    what="Collections"
                    reason={listError}
                    onRetry={() =>
                      void fetchAllCollections(searchFilter(debouncedSearch))
                    }
                  />
                </TableCell>
              </TableRow>
            ) : sorted.length === 0 ? (
              <TableRow>
                <TableCell colSpan={COLUMNS} className="h-48 text-center">
                  <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <ServerStackIcon className="size-10 opacity-40" />
                    <p className="text-sm">
                      {search
                        ? "No Collections match your search."
                        : "No Collections yet."}
                    </p>
                    {!search && (
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
                <TableRow
                  key={`${ok.tenantId}/${ok.collection}`}
                  className="group"
                >
                  <TableCell>
                    <div className="flex items-center gap-3">
                      <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                        <ServerStackIcon className="size-4" />
                      </div>
                      <Link
                        href={collectionHref(ok.tenantId, ok.collection)}
                        className="font-mono text-xs hover:text-primary hover:underline"
                        title={ok.collection}
                      >
                        {ok.collection}
                      </Link>
                      {ok.access === CollectionAccess.PUBLIC_READ && (
                        <PublicReadBadge />
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    <Link
                      href={tenantHref(ok.tenantId)}
                      title={ok.tenantId}
                      className="block max-w-48 truncate text-sm hover:text-primary"
                    >
                      {tenantLabel(tenantNames, ok.tenantId)}
                    </Link>
                  </TableCell>
                  <TableCell className="hidden @xs:table-cell">
                    {ok.displayName || (
                      <span className="text-muted-foreground italic">—</span>
                    )}
                  </TableCell>
                  <TableCell className="hidden @md:table-cell">
                    <BucketNameLink bucket={ok.bucket} />
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
                          <Link
                            href={collectionHref(ok.tenantId, ok.collection)}
                          >
                            View details
                          </Link>
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          variant="destructive"
                          onSelect={() => setDeleteTarget(ok)}
                        >
                          <TrashIcon className="size-4" />
                          Delete Collection
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

      {/* Create dialog */}
      <CollectionCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        backends={backends}
        backendsFailed={failedRead(backendsError, fetchBackends)}
        bucketsHref="/buckets"
        createCollection={createCollection}
        onCreated={() =>
          void fetchAllCollections(searchFilter(debouncedSearch))
        }
      />

      <CollectionDeleteDialog
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
      />
    </div>
  );
}
