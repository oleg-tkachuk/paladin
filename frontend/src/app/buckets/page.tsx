"use client";

import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { EmptyState } from "@/components/ui/EmptyState";
import { emptyReason } from "@/lib/emptyState";
import React, { useEffect, useMemo, useState } from "react";
import Link from "next/link";

import {
  ArchiveBoxIcon,
  ArrowPathIcon,
  ClockIcon,
  EllipsisHorizontalIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useBuckets } from "@/hooks/useBuckets";
import { useBackends } from "@/hooks/useBackends";
import { useTenants } from "@/hooks/useTenants";
import { Bucket } from "@/gen/paladin/admin/v1/types_pb";

import { Button } from "@/components/ui/button";
import { TableToolbar } from "@/components/ui/TableToolbar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { failedRead } from "@/components/ui/ListLoadError";
import { cn } from "@/lib/utils";
import { searchFilter } from "@/lib/cel";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { BucketCreateDialog } from "@/components/features/buckets/BucketCreateDialog";
import { isProvisionInFlight, PROVISION_POLL_MS } from "@/lib/bucketProvision";
import { useRefetchWhile } from "@/hooks/useRefetchWhile";
import { BucketDeleteDialog } from "@/components/features/buckets/BucketDeleteDialog";
import {
  BucketTable,
  BUCKET_FACETS,
  tenantLabelMap,
  BUCKET_SORT_COLUMNS,
  type BucketSortColumn,
} from "@/components/features/buckets/BucketTable";
import { useTableFacets } from "@/hooks/useTableFacets";
import { bucketHref } from "@/lib/navigation/hrefs";
import { TableScope } from "@/components/ui/TableScope";

/** This list's id in the user's table preferences. */
const TABLE_ID = "buckets";

/** The bucket tab the row menu opens. */
const LIFECYCLE_TAB = "lifecycle";

export default function BucketsPage() {
  // The search is server-side: see below.
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  // Trails `search` by 300ms so a read does not fire per keystroke.
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  const {
    buckets,
    loading,
    error: listError,
    refetch,
    createBucket,
    deleteBucket,
  } = useBuckets({ filter: searchFilter(debouncedSearch) });
  const {
    backends: backendRows,
    error: backendsError,
    refetch: refetchBackends,
  } = useBackends();

  // The create dialog's owner picker (ADR-0032: every bucket has one).
  const {
    tenants,
    error: tenantsError,
    refetch: refetchTenants,
  } = useTenants();

  // ─── filters + sort ──────────────────────────────────────────────────────
  //
  // The search is SERVER-side. This page used to fetch every bucket — 859 of
  // them, two requests of 500 — and filter the result in the browser, so the
  // whole table was pulled to answer a five-character query. The filter goes
  // to the API instead, as one conjunct over the derived `search` field so it
  // pushes down to SQL (see src/lib/cel.ts for why it must not be a
  // disjunction). The facets narrow what the search returned: with no search
  // that is every bucket, which the page read before they existed too, and
  // their counts are of that set.
  const { sort, toggleSort: handleSort } = useTableSort<BucketSortColumn>(
    undefined,
    {
      url: PAGE_TABLE,
      tableId: TABLE_ID,
      columns: BUCKET_SORT_COLUMNS,
    },
  );

  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]);

  // ─── create ───────────────────────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false);
  // The palette offers the create while this page is on screen.
  usePageAction({
    id: CREATE_ACTION.bucket,
    label: "New bucket",
    description: "A bucket on a storage backend",
    icon: PlusIcon,
    category: "create",
    perform: () => setCreateOpen(true),
  });

  // ─── delete ───────────────────────────────────────────────────────────────
  const [deleteTarget, setDeleteTarget] = useState<Bucket | null>(null);

  // A created or deleted bucket settles in the reconciler, not in the RPC
  // that started it; without this it read "provisioning…" until a reload.
  useRefetchWhile(
    buckets.some((b) => isProvisionInFlight(b.provisionState)),
    () => void refetch(),
    PROVISION_POLL_MS,
  );

  // The search is not applied again here: re-applying it in the browser would
  // be dead code that quietly disagreed with the API the day the two
  // definitions of "matches" drifted apart.
  const tenantNames = useMemo(() => tenantLabelMap(tenants), [tenants]);
  const facets = useTableFacets(buckets, BUCKET_FACETS, PAGE_TABLE);

  const filtered = useMemo(() => {
    let list = facets.rows;
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      list = [...list].sort((a, b) => {
        const va = sort.column === "backend" ? a.backendId : a.bucketId;
        const vb = sort.column === "backend" ? b.backendId : b.bucketId;
        return va < vb ? -dir : va > vb ? dir : 0;
      });
    }
    return list;
  }, [facets.rows, sort]);

  const hasFilters = search.trim() !== "" || facets.active > 0;
  // The debounce has not fired yet, or its refetch is still running.
  const searchPending = search.trim() !== debouncedSearch.trim() || loading;

  return (
    <TableScope id={TABLE_ID} sort={sort}>
      <div className="space-y-6">
        <PageHeader
          title="Buckets"
          description="Every bucket on every backend: who owns it, and which tenants it is shared with."
          showDefaultActions={false}
          actions={
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <PlusIcon className="size-4" />
              New bucket
            </Button>
          }
        />

        {/* Filter bar */}
        <TableToolbar
          query={search}
          onQueryChange={setSearch}
          searchLabel="Search buckets"
          placeholder="Search by name or display label…"
          facets={facets.controls}
          count={
            buckets.length > 0
              ? { shown: filtered.length, total: buckets.length }
              : undefined
          }
          actions={
            <>
              <Button
                variant="outline"
                size="icon"
                onClick={() => void refetch()}
                aria-label="Refresh"
              >
                <ArrowPathIcon
                  className={cn("size-4", loading && "animate-spin")}
                />
              </Button>
            </>
          }
        />

        <BucketTable
          buckets={filtered}
          tenants={tenantNames}
          loading={loading}
          error={listError}
          onRetry={() => void refetch()}
          sort={sort}
          onSort={handleSort}
          empty={
            <EmptyState
              icon={ArchiveBoxIcon}
              what="buckets"
              // Three states, not two. With the filter server-side an empty
              // table means one of: the debounce has not fired yet, the request
              // is in flight, or the server really found nothing. Collapsing
              // the first two into "no match" tells an operator their bucket is
              // gone while they are still typing its name.
              reason={emptyReason({
                pending: searchPending,
                filtered: hasFilters,
              })}
              purpose="A bucket is a store on a backend that collections keep their objects in."
              action={{
                label: "Create the first bucket",
                onClick: () => setCreateOpen(true),
              }}
            />
          }
          rowActions={(b) => (
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
                  <Link
                    href={bucketHref(b.backendId, b.bucketId, LIFECYCLE_TAB)}
                  >
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
          )}
        />

        <BucketCreateDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          backends={backendRows}
          backendsFailed={failedRead(backendsError, refetchBackends)}
          tenants={tenants}
          tenantsFailed={failedRead(tenantsError, refetchTenants)}
          createBucket={createBucket}
        />

        <BucketDeleteDialog
          target={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          deleteBucket={deleteBucket}
        />
      </div>
    </TableScope>
  );
}

// ProvisionStateBadge mirrors the backend's outbox state machine. Empty
// or "ready" → muted "ready" pill; "pending" / "deleting" → animated
// info pill so the operator sees there's work in flight; "failed" /
// "deletion_failed" → destructive pill so a stuck row stands out at a
// glance. Old rows from before the migration default to "ready" — same
// affordance.
