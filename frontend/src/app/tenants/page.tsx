"use client";

import { tenantDetailQuery } from "@/lib/queries/details";
import { IntentTableRow } from "@/components/ui/IntentTableRow";
import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { ListBody, ListPage } from "@/components/layout/templates/ListPage";
import { emptyReason } from "@/lib/emptyState";
import React, { useMemo, useState } from "react";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { useTableFacets } from "@/hooks/useTableFacets";
import { useTablePage } from "@/hooks/useTablePage";
import { TablePagination } from "@/components/ui/TablePagination";
import type { Facet } from "@/lib/table/facets";
import { PAGE_TABLE } from "@/lib/table/urlState";
import Link from "next/link";
import {
  ArrowPathIcon,
  ArrowRightIcon,
  BuildingOfficeIcon,
  EllipsisHorizontalIcon,
  PencilSquareIcon,
  PlusIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { bucketHref, parseBucketName } from "@/lib/navigation/hrefs";
import { useTenants } from "@/hooks/useTenants";
import { useTenantLimitSignals } from "@/hooks/useTenantLimitSignals";
import { tenantLimitState } from "@/lib/tenant/limits";
import { StatusLabel } from "@/components/ui/StatusIcon";
import { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { TenantCreateDialog } from "./TenantCreateDialog";
import { TenantEditDialog } from "./TenantEditDialog";
import { TenantDeleteDialog } from "./TenantDeleteDialog";

import { Button } from "@/components/ui/button";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { Badge } from "@/components/ui/badge";
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
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import { TableScope } from "@/components/ui/TableScope";
import { NoticeIcon } from "@/components/ui/Notice";
import { noticeFromLabels } from "@/lib/notice";
import { StatusBar, tallyStatus } from "@/components/ui/StatusBar";
import { ViewModeSwitcher } from "@/components/ui/ViewModeSwitcher";
import { useTableViewMode } from "@/hooks/useTableViewMode";
import { plainLabels, tenantStatus } from "./tenantView";
import { TenantSummary, TenantTiles } from "./TenantViews";

/** This list's id in the user's table preferences. */
const TABLE_ID = "tenants";

const SORT_COLUMNS = ["slug", "displayName"] as const;
type SortColumn = (typeof SORT_COLUMNS)[number];

/**
 * Whether a tenant has a default bucket — one without cannot provision a
 * collection until it is given one — and the labels it carries, each as the
 * "key=value" its badge shows. Labels are free-form, so the menu offers the
 * ones the loaded tenants carry.
 */
const TENANT_FACETS: readonly Facet<Tenant>[] = [
  {
    id: "bucket",
    label: "Default bucket",
    values: (t) => [t.defaultBucket ? "set" : "none"],
    options: [
      { value: "set", label: "Set" },
      { value: "none", label: "None" },
    ],
  },
  {
    id: "label",
    label: "Label",
    values: (t) => Object.entries(t.labels).map(([k, v]) => `${k}=${v}`),
  },
];

// Tenant.default_bucket is the resource name of the single (backend, bucket)
// the tenant is bound to: "storageBackends/{backend}/buckets/{bucket}". Empty
// when unset. Split into its parts so the Storage column can render a compact
// "{backend}/{bucket}".

export default function TenantsPage() {
  const {
    tenants,
    loading,
    error: listError,
    refetch: refetchTenants,
    createTenant,
    updateTenantMetadata,
    deleteTenant,
  } = useTenants();
  // Which tenants are near or at their limits. A read that fails or has not
  // landed adds nothing: the tenants show, without the states it would tell.
  const { signals: limits } = useTenantLimitSignals();

  // ─── search + sort ────────────────────────────────────────────────────────
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  const { sort, toggleSort: handleSort } = useTableSort<SortColumn>(undefined, {
    url: PAGE_TABLE,
    tableId: TABLE_ID,
    columns: SORT_COLUMNS,
  });

  // Dialog open/target state. The create/edit/delete form state + handlers
  // live inside their respective Tenant*Dialog components; the page only holds
  // which one is open (set by the row menu / New tenant button).
  const [createOpen, setCreateOpen] = useState(false);
  // The palette offers the create while this page is on screen.
  usePageAction({
    id: CREATE_ACTION.tenant,
    label: "New tenant",
    description: "A workspace with its own policy",
    icon: PlusIcon,
    category: "create",
    perform: () => setCreateOpen(true),
  });
  const [editing, setEditing] = useState<Tenant | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Tenant | null>(null);

  const facets = useTableFacets(tenants, TENANT_FACETS, PAGE_TABLE);
  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    let list = facets.rows;
    if (q) {
      // Search across all three identity fields. Slug is the most
      // common keystroke target, ID is the audit/debug fallback.
      list = list.filter(
        (t) =>
          (t.slug || "").toLowerCase().includes(q) ||
          (t.displayName || "").toLowerCase().includes(q) ||
          t.tenantId.toLowerCase().includes(q),
      );
    }
    if (sort.column && sort.direction) {
      const dir = sort.direction === "asc" ? 1 : -1;
      list = [...list].sort((a, b) => {
        const va =
          sort.column === "slug"
            ? (a.slug || a.tenantId).toLowerCase()
            : (a.displayName || a.slug || a.tenantId).toLowerCase();
        const vb =
          sort.column === "slug"
            ? (b.slug || b.tenantId).toLowerCase()
            : (b.displayName || b.slug || b.tenantId).toLowerCase();
        return va < vb ? -dir : va > vb ? dir : 0;
      });
    }
    return list;
  }, [facets.rows, search, sort]);
  const paged = useTablePage(filtered, {
    url: PAGE_TABLE,
    tableId: TABLE_ID,
    resetOn: [search, facets.selection],
  });

  // Tiles of the page the table would show, or a summary of every row it
  // pages through; the table itself while there are none, so loading, a
  // failure and an empty list read as it says them.
  const { view, setView } = useTableViewMode(TABLE_ID);
  const showRows = filtered.length > 0 && !listError;
  const otherView =
    showRows && view === "tiles" ? (
      <TenantTiles tenants={paged.rows} limits={limits} />
    ) : showRows && view === "summary" ? (
      <TenantSummary tenants={filtered} limits={limits} />
    ) : undefined;

  return (
    <TableScope id={TABLE_ID} sort={sort}>
      <ListPage
        // A summary counts every row, so it has no pages to step through.
        footer={
          view === "summary" ? undefined : (
            <TablePagination {...paged.pagination} />
          )
        }
        header={
          <>
            <PageHeader
              title="Tenants"
              description="Multi-tenant workspaces and inherited Cedar policy."
              showDefaultActions={false}
              actions={
                <>
                  <ViewModeSwitcher value={view} onChange={setView} />
                  <Button size="sm" onClick={() => setCreateOpen(true)}>
                    <PlusIcon className="size-4" />
                    New tenant
                  </Button>
                </>
              }
            />
            <StatusBar
              segments={tallyStatus(filtered, (t) =>
                tenantStatus(t, limits.get(t.tenantId)),
              )}
            />
          </>
        }
        view={otherView}
        toolbar={
          <TableToolbar
            query={search}
            onQueryChange={setSearch}
            searchLabel="Search tenants"
            placeholder="Search tenants by ID or name…"
            facets={facets.controls}
            count={
              tenants.length > 0
                ? { shown: filtered.length, total: tenants.length }
                : undefined
            }
            actions={
              <>
                <Button
                  variant="outline"
                  size="icon"
                  onClick={() => {
                    void refetchTenants();
                  }}
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
                className="@md:w-55"
                label="Slug"
                column="slug"
                current={sort}
                onSort={handleSort}
              />
              <SortableHead
                label="Display name"
                column="displayName"
                current={sort}
                onSort={handleSort}
              />
              <StaticHead className="hidden @4xl:table-cell">
                Default bucket
              </StaticHead>
              <StaticHead className="hidden @md:table-cell">Labels</StaticHead>
              <StaticHead className="hidden @5xl:table-cell w-70">
                Tenant ID
              </StaticHead>
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <ListBody
            colSpan={6}
            loading={loading && tenants.length === 0}
            error={
              listError
                ? {
                    what: "Tenants",
                    reason: listError,
                    onRetry: () => void refetchTenants(),
                  }
                : null
            }
            empty={
              filtered.length === 0
                ? {
                    icon: BuildingOfficeIcon,
                    what: "tenants",
                    reason: emptyReason({
                      filtered: !!search || facets.active > 0,
                    }),
                    onClearFilters: () => {
                      setSearch("");
                      facets.clear();
                    },
                    purpose:
                      "A tenant is one customer kept apart from the rest: its own users, collections, quotas and budget.",
                    action: {
                      label: "Create the first tenant",
                      onClick: () => setCreateOpen(true),
                    },
                  }
                : null
            }
          >
            {paged.rows.map((tenant) => {
              // A notice's labels show as its glyph, not as label badges.
              const labelEntries = plainLabels(tenant.labels);
              const notice = noticeFromLabels(tenant.labels);
              const db = parseBucketName(tenant.defaultBucket);
              const limit = tenantLimitState(limits.get(tenant.tenantId));
              // Slug is the canonical handle in URLs; UUID is the
              // safety net when slug is empty (legacy rows). Phase 0
              // makes slug NOT NULL UNIQUE, so the fallback only
              // matters during the rolling deploy.
              const handle = tenant.slug || tenant.tenantId;
              const detailHref = `/tenants/${encodeURIComponent(handle)}`;
              return (
                <IntentTableRow
                  key={tenant.tenantId}
                  className="group"
                  query={tenantDetailQuery(handle)}
                >
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <Link
                        href={detailHref}
                        className="flex items-center gap-3 hover:text-primary"
                      >
                        <div className="flex size-8 items-center justify-center rounded-md bg-primary/15 text-primary ring-1 ring-primary/30">
                          <BuildingOfficeIcon className="size-4" />
                        </div>
                        <span
                          title={tenant.slug}
                          className="max-w-48 truncate whitespace-nowrap font-medium group-hover:underline"
                        >
                          {tenant.slug || (
                            <span className="text-muted-foreground italic">
                              (no slug)
                            </span>
                          )}
                        </span>
                      </Link>
                      {/* Beside the link, not in it: the glyph takes focus. */}
                      {notice && <NoticeIcon notice={notice} />}
                      {limit && (
                        <StatusLabel
                          tone={limit.tone}
                          className="whitespace-nowrap text-xs"
                        >
                          {limit.label}
                        </StatusLabel>
                      )}
                    </div>
                  </TableCell>
                  {/* Free text, and the one column that gives: it takes what
                      the others leave and truncates there, one line a row,
                      with the whole name in the tooltip. */}
                  <TableCell className="w-full max-w-0">
                    <Link
                      href={detailHref}
                      title={tenant.displayName || undefined}
                      className="block truncate hover:text-primary hover:underline"
                    >
                      {tenant.displayName || (
                        <span className="text-muted-foreground italic">
                          (unnamed)
                        </span>
                      )}
                    </Link>
                  </TableCell>
                  <TableCell className="hidden @4xl:table-cell">
                    {db ? (
                      <Link
                        href={bucketHref(db.backendId, db.bucketId)}
                        title={tenant.defaultBucket}
                        className={cn(
                          T.codeSmall,
                          "block max-w-40 truncate hover:text-primary",
                        )}
                      >
                        {db.bucketId}
                      </Link>
                    ) : (
                      <span className="text-xs text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell className="hidden @md:table-cell">
                    {labelEntries.length === 0 ? (
                      <span className="text-xs text-muted-foreground">—</span>
                    ) : (
                      <div className="flex flex-wrap gap-1">
                        {labelEntries.slice(0, 3).map(([k, v]) => (
                          // Labels are case-sensitive data: shown as stored.
                          <Badge
                            key={k}
                            variant="outline"
                            title={`${k}=${String(v)}`}
                            className={cn(T.codeSmall, "max-w-40 truncate")}
                          >
                            {k}={String(v)}
                          </Badge>
                        ))}
                        {labelEntries.length > 3 && (
                          <Badge variant="secondary">
                            +{labelEntries.length - 3}
                          </Badge>
                        )}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="hidden @5xl:table-cell">
                    <span
                      className="font-mono text-sm text-muted-foreground"
                      title={tenant.tenantId}
                    >
                      {tenant.tenantId.slice(0, 8)}…{tenant.tenantId.slice(-4)}
                    </span>
                  </TableCell>
                  <TableCell className="text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8 opacity-60 group-hover:opacity-100"
                          aria-label={`Actions for ${tenant.tenantId}`}
                        >
                          <EllipsisHorizontalIcon className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem asChild>
                          <Link href={detailHref}>
                            <ArrowRightIcon className="size-4" />
                            Open tenant
                          </Link>
                        </DropdownMenuItem>
                        <DropdownMenuItem onSelect={() => setEditing(tenant)}>
                          <PencilSquareIcon className="size-4" />
                          Edit tenant
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          variant="destructive"
                          onSelect={() => setDeleteTarget(tenant)}
                        >
                          <TrashIcon className="size-4" />
                          Delete tenant
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </IntentTableRow>
              );
            })}
          </ListBody>
        </Table>
      </ListPage>

      {/* ─── Create dialog ───────────────────────────────────────────────── */}
      <TenantCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        createTenant={createTenant}
      />

      {/* ─── Edit dialog ─────────────────────────────────────────────────── */}
      <TenantEditDialog
        editing={editing}
        onClose={() => setEditing(null)}
        updateTenantMetadata={updateTenantMetadata}
      />

      {/* ─── Delete confirmation ─────────────────────────────────────────── */}
      <TenantDeleteDialog
        deleteTarget={deleteTarget}
        onClose={() => setDeleteTarget(null)}
        deleteTenant={deleteTenant}
      />
    </TableScope>
  );
}
