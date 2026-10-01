"use client";

import React, { useEffect, useMemo, useState } from "react";
import { useTableSort, type SortState } from "@/hooks/useTableSort";
import Link from "next/link";
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

import { PageHeader } from "@/components/layout/PageHeader";
import { useCollections } from "@/hooks/useCollections";
import { useBackends } from "@/hooks/useBackends";
import { useScope } from "@/context/ScopeContext";
import { useNotification } from "@/components/ui/Notification";

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
import { failedRead, ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { searchFilter } from "@/lib/cel";
import { T } from "@/lib/ui/typography";
import { CollectionCreateDialog } from "@/components/features/collections/CollectionCreateDialog";

type SortColumn = "name" | "displayName" | "backendId";

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

export default function CollectionsPage() {
  const {
    collections,
    loading,
    error: listError,
    fetchCollections,
    createCollection,
    deleteCollection,
  } = useCollections();
  const {
    backends: backendRows,
    error: backendsError,
    fetchBackends,
  } = useBackends();
  const { tenantId } = useScope();
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
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

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
    fetchCollections(searchFilter(debouncedSearch));
  }, [fetchCollections, debouncedSearch]);

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
      const target = collections.find((c) => c.collection === deleteTarget);
      await deleteCollection(deleteTarget, target?.resourceVersion ?? "");
      showNotification({
        type: "success",
        title: "Collection deleted",
        message: deleteTarget,
      });
      setDeleteTarget(null);
    } catch {
      showNotification({
        type: "error",
        title: "Deletion failed",
        message: "Collection must be empty before it can be removed.",
      });
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Collections"
        description="Tenant-scoped namespaces routed to a physical bucket."
        showDefaultActions={false}
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" />
            New Collection
          </Button>
        }
      />

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
          onClick={() => fetchCollections(searchFilter(debouncedSearch))}
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
                  label="Backend"
                  column="backendId"
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
            {loading && collections.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={4} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : listError ? (
              <TableRow>
                <TableCell colSpan={4} className="h-48 text-center">
                  <ListLoadError
                    what="Collections"
                    reason={listError}
                    onRetry={() => void fetchCollections()}
                  />
                </TableCell>
              </TableRow>
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
                <TableRow key={ok.collection} className="group">
                  <TableCell>
                    <div className="flex items-center gap-3">
                      <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                        <ServerStackIcon className="size-4" />
                      </div>
                      <Link
                        href={`/tenants/${encodeURIComponent(ok.tenantId || tenantId || "")}/collections/${encodeURIComponent(ok.collection)}`}
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
                    {/* backendId removed from Collection schema — show bucket binding instead */}
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
                          <Link
                            href={`/tenants/${encodeURIComponent(ok.tenantId || tenantId || "")}/collections/${encodeURIComponent(ok.collection)}`}
                          >
                            View details
                          </Link>
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          variant="destructive"
                          onSelect={() => setDeleteTarget(ok.collection)}
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
        onCreated={() => void fetchCollections(searchFilter(debouncedSearch))}
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
