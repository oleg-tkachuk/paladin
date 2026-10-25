"use client";

// /tenants/<id>/buckets — the buckets this tenant may bind Collections to.
//
// Asks BucketService.ListAccessibleBuckets: the buckets the tenant owns plus
// the ones their owners granted it (ADR-0032). Every bucket it stores in is
// one of those — a default binding and a collection both need the right to
// bind — so nothing it uses is missing. Ownership alone is not the question:
// a shared-layout tenant (ADR-0015) owns no bucket and binds granted ones.
//
// An owned row links to the bucket detail subtree under this tenant. A
// granted row belongs to another tenant: its lifecycle rules, deletion and
// grants are its owner's, so it links to the storage-first bucket page
// instead of into a subtree that would redirect to the owner's.
//
// Creating follows the layout. A dedicated tenant's new bucket is owned by it,
// so it lands on this list. A shared tenant binds to buckets it is granted:
// the page sends the operator to /buckets, where a bucket's page grants it.

import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { useTenantChangesBlocked } from "../tenant-context";
import React, { useMemo, useState } from "react";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { PAGE_TABLE } from "@/lib/table/urlState";
import Link from "next/link";

import {
  ArchiveBoxIcon,
  ArrowPathIcon,
  ClockIcon,
  EllipsisHorizontalIcon,
  PlusIcon,
  ServerStackIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { useBuckets } from "@/hooks/useBuckets";
import { useBackends } from "@/hooks/useBackends";
import { Bucket } from "@/gen/paladin/admin/v1/types_pb";

import { Button } from "@/components/ui/button";
import { TableToolbar } from "@/components/ui/TableToolbar";
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
import { BucketCreateDialog } from "@/components/features/buckets/BucketCreateDialog";
import { isDedicatedLayout } from "@/lib/storageLayout";
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
import { useTablePage } from "@/hooks/useTablePage";
import { TablePagination } from "@/components/ui/TablePagination";
import { useTenants } from "@/hooks/useTenants";
import { bucketHref } from "@/lib/navigation/hrefs";

/** The bucket tab the row menu opens. */
const LIFECYCLE_TAB = "lifecycle";

/** The cross-tenant bucket index; a bucket's page there grants it. */
const BUCKET_INDEX_HREF = "/buckets";

/** ListAccessibleBuckets addresses the tenant by resource name. */
const tenantResourceName = (tenantId: string) => `tenants/${tenantId}`;

export default function TenantBucketsPage() {
  const changesBlocked = useTenantChangesBlocked();
  const tenant = useTenant();
  const {
    buckets,
    loading,
    error: bucketsError,
    refetch,
    createBucket,
    deleteBucket,
    // Owned plus granted, across backends.
  } = useBuckets({ accessibleTo: tenantResourceName(tenant.tenantId) });
  const {
    backends: backendRows,
    error: backendsError,
    refetch: refetchBackends,
  } = useBackends();

  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  const { sort, toggleSort: handleSort } = useTableSort<BucketSortColumn>(
    undefined,
    {
      url: PAGE_TABLE,
      columns: BUCKET_SORT_COLUMNS,
    },
  );

  const [createOpen, setCreateOpen] = useState(false);

  const [deleteTarget, setDeleteTarget] = useState<Bucket | null>(null);

  const ownedByTenant = (b: Bucket) => b.ownerTenantId === tenant.tenantId;
  const dedicated = isDedicatedLayout(tenant.storageLayout);
  // The palette offers the create while this page does.
  usePageAction(
    dedicated && !changesBlocked
      ? {
          id: CREATE_ACTION.tenantBucket,
          label: "New bucket",
          description: "A bucket of this tenant's own",
          icon: PlusIcon,
          category: "create",
          perform: () => setCreateOpen(true),
        }
      : null,
  );

  // A created or deleted bucket settles in the reconciler, not in the RPC
  // that started it; without this it read "provisioning…" until a reload.
  useRefetchWhile(
    buckets.some((b) => isProvisionInFlight(b.provisionState)),
    () => void refetch(),
    PROVISION_POLL_MS,
  );

  // Owners and grantees by name: every tenant a platform admin may list, and
  // this one, which a tenant's own admin can read when it may list no other.
  const { tenants } = useTenants();
  const tenantNames = useMemo(
    () =>
      tenantLabelMap([
        ...tenants,
        {
          tenantId: tenant.tenantId,
          slug: tenant.slug,
          displayName: tenant.displayName,
        },
      ]),
    [tenants, tenant.tenantId, tenant.slug, tenant.displayName],
  );

  const facets = useTableFacets(buckets, BUCKET_FACETS, PAGE_TABLE);
  const filtered = useMemo(() => {
    let list = facets.rows;
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
        const va = sort.column === "backend" ? a.backendId : a.bucketId;
        const vb = sort.column === "backend" ? b.backendId : b.bucketId;
        return va < vb ? -dir : va > vb ? dir : 0;
      });
    }
    return list;
  }, [facets.rows, search, sort]);
  const paged = useTablePage(filtered, {
    url: PAGE_TABLE,
    resetOn: [search, facets.selection],
  });
  const hasFilters = search.trim() !== "" || facets.active > 0;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Buckets</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            S3 buckets this tenant may bind collections to: the ones it owns and
            the ones their owners granted it. A bucket is granted from its page
            in the cross-tenant index at{" "}
            <Link
              href="/buckets"
              className="text-primary hover:underline font-mono"
            >
              /buckets
            </Link>
            .
          </p>
        </div>
        {dedicated ? (
          <Button
            size="sm"
            onClick={() => setCreateOpen(true)}
            disabled={Boolean(changesBlocked)}
            title={changesBlocked ?? undefined}
          >
            <PlusIcon className="size-4" />
            New bucket
          </Button>
        ) : (
          <Button size="sm" variant="outline" asChild>
            <Link href={BUCKET_INDEX_HREF}>
              <ServerStackIcon className="size-4" />
              All buckets
            </Link>
          </Button>
        )}
      </div>

      {/* Filter bar */}
      <TableToolbar
        query={search}
        onQueryChange={setSearch}
        searchLabel="Search buckets"
        placeholder="Search by name or display label…"
        facets={facets.controls}
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
        buckets={paged.rows}
        tenants={tenantNames}
        perspective={tenant.tenantId}
        loading={loading}
        error={bucketsError}
        onRetry={() => void refetch()}
        sort={sort}
        onSort={handleSort}
        empty={
          <div className="flex flex-col items-center gap-3 text-muted-foreground">
            <ArchiveBoxIcon className="size-10 opacity-40" />
            <p className="text-sm">
              {hasFilters
                ? "No buckets match your filters."
                : dedicated
                  ? "This tenant may bind to no bucket yet: it owns none and holds no grant."
                  : "This tenant may bind to no bucket yet: it holds no grant. A bucket's owner grants it from the bucket's page."}
            </p>
            {!hasFilters &&
              (dedicated ? (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setCreateOpen(true)}
                  disabled={Boolean(changesBlocked)}
                  title={changesBlocked ?? undefined}
                >
                  <PlusIcon className="size-4" />
                  Create the first bucket
                </Button>
              ) : (
                <Button size="sm" variant="outline" asChild>
                  <Link href={BUCKET_INDEX_HREF}>
                    <ServerStackIcon className="size-4" />
                    Find a bucket to grant
                  </Link>
                </Button>
              ))}
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
              {ownedByTenant(b) ? (
                <>
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
                </>
              ) : (
                // Another tenant's: its rules, deletion and grants are the
                // owner's, and reach every tenant in it.
                <DropdownMenuItem asChild>
                  <Link href={bucketHref(b.backendId, b.bucketId)}>
                    <ServerStackIcon className="size-4" />
                    Open the bucket
                  </Link>
                </DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      />
      <TablePagination {...paged.pagination} />

      {dedicated && (
        <BucketCreateDialog
          open={createOpen}
          onOpenChange={setCreateOpen}
          backends={backendRows}
          backendsFailed={failedRead(backendsError, refetchBackends)}
          description={`Created on the backend and owned by ${tenant.displayName || tenant.slug}. Other tenants bind to it only once granted.`}
          fixedOwnerTenantId={tenant.tenantId}
          createBucket={createBucket}
        />
      )}

      <BucketDeleteDialog
        target={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        deleteBucket={deleteBucket}
      />
    </div>
  );
}
