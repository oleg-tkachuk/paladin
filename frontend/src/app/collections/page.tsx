"use client";

import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { ListBody, ListPage } from "@/components/layout/templates/ListPage";
import { emptyReason } from "@/lib/emptyState";
import { useRouter } from "next/navigation";

import { CollectionAccess } from "@/gen/paladin/admin/v1/types_pb";
import { PublicReadBadge } from "@/components/features/buckets/PublicReadBadge";
import React, { useEffect, useMemo, useState } from "react";
import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { PAGE_TABLE } from "@/lib/table/urlState";
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
import { collectionDeleteFailure } from "@/lib/collectionDeleteFailure";
import { useNotification } from "@/components/ui/Notification";
import { TableToolbar } from "@/components/ui/TableToolbar";

import { Button } from "@/components/ui/button";
import {
  Table,
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
import { failedRead } from "@/components/ui/ListLoadError";
import { cn } from "@/lib/utils";
import { searchFilter } from "@/lib/cel";
import { CollectionCreateDialog } from "@/components/features/collections/CollectionCreateDialog";
import { CollectionDeleteDialog } from "@/components/features/collections/CollectionDeleteDialog";
import {
  UsageCells,
  UsageHeads,
  USAGE_COLUMNS,
} from "@/components/features/collections/CollectionUsage";
import { COLLECTION_SORT_COLUMNS, sortCollections } from "@/lib/collectionSort";
import { SortableHead } from "@/components/ui/SortHeader";
import { TableScope } from "@/components/ui/TableScope";
import { TenantHoverCard } from "@/components/features/hover/ResourceHoverCards";

/** This list's id in the user's table preferences. */
const TABLE_ID = "collections";

const SORT_COLUMNS = COLLECTION_SORT_COLUMNS;
type SortColumn = (typeof SORT_COLUMNS)[number];

/** Collection, tenant, display name, bucket, the usage columns, actions. */
const COLUMNS = 5 + USAGE_COLUMNS;

export default function CollectionsPage() {
  // The search is the server's filter; the list follows it through the cache.
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  const {
    collections,
    loading,
    error: listError,
    refetch: refetchCollections,
    createCollection,
    deleteCollection,
  } = useCollections({
    catalogue: true,
    filter: searchFilter(debouncedSearch),
  });
  // The rows span tenants; each names its own.
  const { tenants } = useTenants();
  const tenantNames = useMemo(() => tenantLabelMap(tenants), [tenants]);
  const {
    backends: backendRows,
    error: backendsError,
    refetch: refetchBackends,
  } = useBackends();
  const { showNotification } = useNotification();
  const router = useRouter();

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
  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>(undefined, {
    url: PAGE_TABLE,
    tableId: TABLE_ID,
    columns: SORT_COLUMNS,
  });

  // ── create
  const [createOpen, setCreateOpen] = useState(false);
  // The palette offers the create while this page is on screen.
  usePageAction({
    id: CREATE_ACTION.collection,
    label: "New collection",
    description: "A collection in your tenant",
    icon: PlusIcon,
    category: "create",
    perform: () => setCreateOpen(true),
  });

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

  const sorted = useMemo(
    () => sortCollections(collections, sort.column, sort.direction),
    [collections, sort],
  );

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
      // a stale version another. When the trash holds it, the notice opens it.
      showNotification(
        collectionDeleteFailure(err, deleteTarget, (href) => router.push(href)),
      );
    }
  };

  return (
    <TableScope id={TABLE_ID} sort={sort}>
      <ListPage
        header={
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
        }
        toolbar={
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
                  onClick={() => refetchCollections()}
                  aria-label="Refresh"
                >
                  <ArrowPathIcon
                    className={cn("size-4", loading && "animate-spin")}
                  />
                </Button>
              </>
            }
          />
        }
      >
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
                className="hidden @3xl:table-cell"
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
              <UsageHeads current={sort} onSort={handleSort} />
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <ListBody
            colSpan={COLUMNS}
            loading={loading && collections.length === 0}
            error={
              listError
                ? {
                    what: "Collections",
                    reason: listError,
                    onRetry: () => refetchCollections(),
                  }
                : null
            }
            empty={
              sorted.length === 0
                ? {
                    icon: ServerStackIcon,
                    what: "collections",
                    reason: emptyReason({ filtered: !!search }),
                    purpose:
                      "A collection is a tenant's namespace of objects, stored in one bucket.",
                    action: {
                      label: "Provision the first collection",
                      onClick: () => setCreateOpen(true),
                    },
                  }
                : null
            }
          >
            {sorted.map((ok) => (
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
                      className="block max-w-48 truncate font-mono text-xs hover:text-primary hover:underline"
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
                  <TenantHoverCard handle={ok.tenantId}>
                    <Link
                      href={tenantHref(ok.tenantId)}
                      title={ok.tenantId}
                      className="block max-w-48 truncate text-sm hover:text-primary"
                    >
                      {tenantLabel(tenantNames, ok.tenantId)}
                    </Link>
                  </TenantHoverCard>
                </TableCell>
                <TableCell className="hidden @3xl:table-cell">
                  {ok.displayName || (
                    <span className="text-muted-foreground italic">—</span>
                  )}
                </TableCell>
                {/* The one column that gives: it takes what the others leave and
                    truncates there, so no width overflows the card. */}
                <TableCell className="hidden w-full max-w-0 @md:table-cell">
                  <BucketNameLink bucket={ok.bucket} />
                </TableCell>
                <UsageCells collection={ok} />
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
                        <Link href={collectionHref(ok.tenantId, ok.collection)}>
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
            ))}
          </ListBody>
        </Table>
      </ListPage>

      {/* Create dialog */}
      <CollectionCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        backends={backends}
        backendsFailed={failedRead(backendsError, refetchBackends)}
        bucketsHref="/buckets"
        createCollection={createCollection}
        onCreated={() => refetchCollections()}
      />

      <CollectionDeleteDialog
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
      />
    </TableScope>
  );
}
