"use client";

import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { IntentTableRow } from "@/components/ui/IntentTableRow";
import { collectionDetailQuery } from "@/lib/queries/details";
import { ListBody, ListPage } from "@/components/layout/templates/ListPage";
import { emptyReason } from "@/lib/emptyState";
import { useRouter } from "next/navigation";

// /tenants/<id>/collections — tenant-scoped Collection list.
//
// The list is read here with `parent = tenants/<tenantId>` from the route;
// Create and Delete go through useCollections, which acts on the same routed
// tenant (useActingTenantId), so a platform admin manages another tenant's
// collections from its page.

import { useTenantChangesBlocked } from "../tenant-context";
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

import { useCollections } from "@/hooks/useCollections";
import { useBackends } from "@/hooks/useBackends";
import { collectionDeleteFailure } from "@/lib/collectionDeleteFailure";
import { useNotification } from "@/components/ui/Notification";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { useTableFacets } from "@/hooks/useTableFacets";
import { TENANT_COLLECTION_FACETS } from "@/lib/collectionFacets";

import { searchFilter } from "@/lib/cel";

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
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../tenant-context";
import { failedRead } from "@/components/ui/ListLoadError";
import { CollectionCreateDialog } from "@/components/features/collections/CollectionCreateDialog";
import { CollectionDeleteDialog } from "@/components/features/collections/CollectionDeleteDialog";
import {
  UsageCells,
  UsageHeads,
  USAGE_COLUMNS,
} from "@/components/features/collections/CollectionUsage";
import { COLLECTION_SORT_COLUMNS, sortCollections } from "@/lib/collectionSort";
import { SortableHead } from "@/components/ui/SortHeader";
import { BucketNameLink } from "@/components/features/buckets/BucketNameLink";
import { TableScope } from "@/components/ui/TableScope";

/** This list's id in the user's table preferences. */
const TABLE_ID = "tenantCollections";

const SORT_COLUMNS = COLLECTION_SORT_COLUMNS;
type SortColumn = (typeof SORT_COLUMNS)[number];

export default function TenantCollectionsPage() {
  const changesBlocked = useTenantChangesBlocked();
  const tenant = useTenant();
  const { createCollection, deleteCollection } = useCollections();
  const {
    backends: backendRows,
    error: backendsError,
    refetch: refetchBackends,
  } = useBackends();
  const { showNotification } = useNotification();
  const router = useRouter();

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

  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  // Trails `search` by 300ms; celFilter is in the react-query key, so without
  // this every keystroke was its own list request.
  const [debouncedSearch, setDebouncedSearch] = useState(search);

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

  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>(undefined, {
    url: PAGE_TABLE,
    tableId: TABLE_ID,
    columns: SORT_COLUMNS,
  });

  // This tenant's collections the search admits, from the shared cache: a
  // delete made here or anywhere else shows at once. The hook keeps the last
  // result of this tenant on screen while a new search loads.
  const {
    collections: list,
    loading,
    error: listError,
    refetch: refetchList,
  } = useCollections({ filter: celFilter });
  const fetchList = (_filter?: string) => refetchList();

  // ── create dialog
  const [createOpen, setCreateOpen] = useState(false);
  // The palette offers the create while this page does.
  usePageAction(
    !changesBlocked
      ? {
          id: CREATE_ACTION.tenantCollection,
          label: "New collection",
          description: "A collection of this tenant, routed to a bucket",
          icon: PlusIcon,
          category: "create",
          perform: () => setCreateOpen(true),
        }
      : null,
  );

  // ── delete confirm
  // The row as it was when Delete was chosen. Its resourceVersion is the OCC
  // guard; looking it up again at confirm read whatever the list held then —
  // nothing, if a search had just changed the query — and sent an empty one.
  const [deleteTarget, setDeleteTarget] = useState<Collection | null>(null);

  // Counted over the tenant's every collection, which the list loads whole.
  const facets = useTableFacets(list, TENANT_COLLECTION_FACETS, PAGE_TABLE);
  const sorted = useMemo(
    () => sortCollections(facets.rows, sort.column, sort.direction),
    [facets.rows, sort],
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
      );
      showNotification({
        type: "success",
        title: "Collection deleted",
        message: deleteTarget.collection,
      });
      setDeleteTarget(null);
      void fetchList(search);
    } catch (err) {
      // The server's reason: a collection that still holds objects is one,
      // a stale version another. When the trash holds it, the notice opens it.
      showNotification(
        collectionDeleteFailure(err, deleteTarget, (href) => router.push(href)),
      );
    }
  };

  const detailHref = (collection: string) =>
    `/tenants/${tenant.slug}/collections/${encodeURIComponent(collection)}`;

  return (
    <TableScope id={TABLE_ID} sort={sort}>
      <ListPage
        header={
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 className="text-lg font-semibold">Collections</h2>
              <p className={cn(T.helper, "max-w-prose")}>
                Tenant-scoped namespaces routed to a physical bucket.
              </p>
            </div>
            <Button
              size="sm"
              onClick={() => setCreateOpen(true)}
              disabled={Boolean(changesBlocked)}
              title={changesBlocked ?? undefined}
            >
              <PlusIcon className="size-4" />
              New Collection
            </Button>
          </div>
        }
        toolbar={
          <TableToolbar
            query={search}
            onQueryChange={setSearch}
            searchLabel="Search collections"
            placeholder="Search by name or display label…"
            facets={facets.controls}
            count={{ shown: sorted.length, total: list.length }}
            actions={
              <>
                <Button
                  variant="outline"
                  size="icon"
                  onClick={() => void fetchList(search)}
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
              <UsageHeads current={sort} onSort={handleSort} />
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <ListBody
            colSpan={4 + USAGE_COLUMNS}
            loading={loading && list.length === 0}
            // "No Collections yet" for a failed list invites a duplicate.
            error={
              listError && list.length === 0
                ? {
                    what: "Collections",
                    reason: listError,
                    onRetry: refetchList,
                  }
                : null
            }
            empty={
              sorted.length === 0
                ? {
                    icon: ServerStackIcon,
                    what: "collections",
                    reason: emptyReason({
                      filtered: !!search || facets.active > 0,
                    }),
                    purpose:
                      "A collection is a tenant's namespace of objects, stored in one bucket.",
                    action: {
                      label: "Provision the first collection",
                      onClick: () => setCreateOpen(true),
                      disabled: Boolean(changesBlocked),
                      title: changesBlocked ?? undefined,
                    },
                  }
                : null
            }
          >
            {sorted.map((ok) => (
              <IntentTableRow
                key={ok.collection}
                className="group"
                query={collectionDetailQuery(tenant.tenantId, ok.collection)}
              >
                <TableCell>
                  <div className="flex items-center gap-3">
                    <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                      <ServerStackIcon className="size-4" />
                    </div>
                    <Link
                      href={detailHref(ok.collection)}
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
                <TableCell className="hidden @xs:table-cell whitespace-normal break-words">
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
                        <Link href={detailHref(ok.collection)}>
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
              </IntentTableRow>
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
        bucketsHref={`/tenants/${tenant.slug}/buckets`}
        createCollection={createCollection}
        onCreated={() => void fetchList(search)}
      />

      <CollectionDeleteDialog
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
      />
    </TableScope>
  );
}
