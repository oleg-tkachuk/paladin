"use client";

import React, { useCallback, useEffect, useMemo, useState } from "react";
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
import {
  SelectRoot,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import { failedRead } from "@/components/ui/ListLoadError";
import { cn } from "@/lib/utils";
import { searchFilter } from "@/lib/cel";
import { useTableSort } from "@/hooks/useTableSort";
import { BucketCreateDialog } from "@/components/features/buckets/BucketCreateDialog";
import { isProvisionInFlight, PROVISION_POLL_MS } from "@/lib/bucketProvision";
import { useRefetchWhile } from "@/hooks/useRefetchWhile";
import { BucketDeleteDialog } from "@/components/features/buckets/BucketDeleteDialog";
import {
  BucketTable,
  SHARING,
  bySharing,
  tenantLabelMap,
  type BucketSortColumn,
  type Sharing,
} from "@/components/features/buckets/BucketTable";
import { bucketHref } from "@/lib/navigation/hrefs";

/** The bucket tab the row menu opens. */
const LIFECYCLE_TAB = "lifecycle";

const ALL_BACKENDS = "__all__";

export default function BucketsPage() {
  const {
    buckets,
    loading,
    error: listError,
    fetchBuckets,
    createBucket,
    deleteBucket,
  } = useBuckets();
  const {
    backends: backendRows,
    error: backendsError,
    fetchBackends,
  } = useBackends();

  // The create dialog's owner picker (ADR-0032: every bucket has one).
  const { tenants, error: tenantsError, fetchTenants } = useTenants();
  useEffect(() => {
    void fetchTenants();
  }, [fetchTenants]);

  const backends = useMemo(
    () => backendRows.map((b) => b.backendId),
    [backendRows],
  );

  // ─── filters + sort ──────────────────────────────────────────────────────
  //
  // Search and the backend picker are SERVER-side now. This page used to fetch
  // every bucket — 859 of them, two requests of 500 — and filter the result in
  // the browser, so the whole table was pulled to answer a five-character
  // query. The filter goes to the API instead, as one conjunct over the derived
  // `search` field so it pushes down to SQL (see src/lib/cel.ts for why it must
  // not be a disjunction).
  const [search, setSearch] = useState("");
  // Trails `search` by 300ms so a refetch does not fire per keystroke — the
  // same shape the objects page uses.
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [filterBackend, setFilterBackend] = useState<string>(ALL_BACKENDS);
  const { sort, toggleSort: handleSort } = useTableSort<BucketSortColumn>();

  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]);

  // ─── create ───────────────────────────────────────────────────────────────
  const [createOpen, setCreateOpen] = useState(false);

  // ─── delete ───────────────────────────────────────────────────────────────
  const [deleteTarget, setDeleteTarget] = useState<Bucket | null>(null);

  // One place that knows how to ask, so the Refresh button and the error
  // retry cannot drift into fetching something other than what is on screen.
  const refetch = useCallback(
    () =>
      fetchBuckets(
        filterBackend === ALL_BACKENDS ? undefined : filterBackend,
        searchFilter(debouncedSearch),
      ),
    [fetchBuckets, filterBackend, debouncedSearch],
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

  // Sort only. The two filters that used to live here are server-side now;
  // re-applying them in the browser would be dead code that quietly disagreed
  // with the API the day the two definitions of "matches" drifted apart.
  const tenantNames = useMemo(() => tenantLabelMap(tenants), [tenants]);
  const [sharing, setSharing] = useState<Sharing>(SHARING.all);

  const filtered = useMemo(() => {
    let list = bySharing(buckets, sharing);
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      list = [...list].sort((a, b) => {
        const va = sort.column === "backend" ? a.backendId : a.bucketId;
        const vb = sort.column === "backend" ? b.backendId : b.bucketId;
        return va < vb ? -dir : va > vb ? dir : 0;
      });
    }
    return list;
  }, [buckets, sort, sharing]);

  const hasFilters =
    search.trim() !== "" ||
    filterBackend !== ALL_BACKENDS ||
    sharing !== SHARING.all;
  // The debounce has not fired yet, or its refetch is still running.
  const searchPending = search.trim() !== debouncedSearch.trim() || loading;

  return (
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
      >
        <SelectRoot value={filterBackend} onValueChange={setFilterBackend}>
          <SelectTrigger aria-label="Filter by backend" className="w-50">
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
        <SelectRoot
          value={sharing}
          onValueChange={(v) => setSharing(v as Sharing)}
        >
          <SelectTrigger aria-label="Filter by sharing" className="w-44">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={SHARING.all}>Shared or not</SelectItem>
            <SelectItem value={SHARING.shared}>Shared</SelectItem>
            <SelectItem value={SHARING.ownerOnly}>Owner only</SelectItem>
          </SelectContent>
        </SelectRoot>
      </TableToolbar>

      <BucketTable
        buckets={filtered}
        tenants={tenantNames}
        loading={loading}
        error={listError}
        onRetry={() => void refetch()}
        sort={sort}
        onSort={handleSort}
        empty={
          <div className="flex flex-col items-center gap-3 text-muted-foreground">
            <ArchiveBoxIcon className="size-10 opacity-40" />
            <p className="text-sm">
              {/*
                Three states, not two. With the filter server-side an empty
                table means one of: the debounce has not fired yet, the request
                is in flight, or the server really found nothing. Collapsing
                the first two into "No buckets match" tells an operator their
                bucket is gone while they are still typing its name.
              */}
              {searchPending
                ? "Searching…"
                : hasFilters
                  ? "No buckets match your filters."
                  : "No buckets yet."}
            </p>
            {!searchPending && !hasFilters && (
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
                <Link href={bucketHref(b.backendId, b.bucketId, LIFECYCLE_TAB)}>
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
        backendsFailed={failedRead(backendsError, fetchBackends)}
        tenants={tenants}
        tenantsFailed={failedRead(tenantsError, fetchTenants)}
        createBucket={createBucket}
      />

      <BucketDeleteDialog
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        deleteBucket={deleteBucket}
      />
    </div>
  );
}

// ProvisionStateBadge mirrors the backend's outbox state machine. Empty
// or "ready" → muted "ready" pill; "pending" / "deleting" → animated
// info pill so the operator sees there's work in flight; "failed" /
// "deletion_failed" → destructive pill so a stuck row stands out at a
// glance. Old rows from before the migration default to "ready" — same
// affordance.
