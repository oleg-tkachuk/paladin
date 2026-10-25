"use client";

// Tenant-scoped audit log. The same table as the cross-tenant /audit page
// (AuditLogTable); the difference is the tenant's trail, which the server
// selects (ListAuditLogRequest.tenant_id): entries the tenant's
// principals made, and entries on its resources — a platform admin's
// work inside the tenant included.
//
// Free-text search filters client-side over the loaded page, same
// affordance as /audit, and a row opens the entry beside the log, as there.

import { ListPage } from "@/components/layout/templates/ListPage";
import { useMemo } from "react";
import Link from "next/link";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { useServerFacets } from "@/hooks/useTableFacets";
import { AUDIT_FACETS, auditFacetCounts } from "@/lib/auditFacets";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { AUDIT_ORDER_FIELDS, type AuditOrderField } from "@/lib/auditOrder";
import { TableToolbar } from "@/components/ui/TableToolbar";
import {
  ArrowPathIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { useAuditLogs } from "@/hooks/useAuditLogs";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../tenant-context";
import { TableScope } from "@/components/ui/TableScope";
import { AuditLogTable } from "@/components/features/audit/AuditLogTable";
import { useDetailPanel } from "@/hooks/useDetailPanel";
import {
  AUDIT_ENTRY_TABS,
  AuditEntryPanel,
} from "@/components/features/audit/AuditEntryPanel";

/** This list's id in the user's table preferences. */
const TABLE_ID = "tenantAudit";

/** Entries per page of the tenant's trail. */
const TENANT_AUDIT_PAGE_SIZE = 100;

export default function TenantAuditLogPage() {
  const tenant = useTenant();

  // Sorted on the server: the log loads a page at a time, so a sort in the
  // browser would order only what has loaded. A new sort starts over.
  const { sort, toggleSort } = useTableSort<AuditOrderField>(undefined, {
    url: PAGE_TABLE,
    tableId: TABLE_ID,
    columns: AUDIT_ORDER_FIELDS,
  });
  // The facets narrow the trail on the server, which counts them with its
  // first page.
  const facets = useServerFacets(AUDIT_FACETS, PAGE_TABLE);
  const {
    entries,
    facetCounts,
    loading,
    error,
    nextCursor,
    refresh,
    loadMore,
  } = useAuditLogs(
    TENANT_AUDIT_PAGE_SIZE,
    "",
    tenant.tenantId,
    sort,
    facets.selection,
  );
  const { controls: controlsFor } = facets;
  const facetControls = useMemo(
    () => controlsFor(auditFacetCounts(facetCounts)),
    [controlsFor, facetCounts],
  );

  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  // The open entry and its tab are in the URL, as on /audit.
  const detail = useDetailPanel(AUDIT_ENTRY_TABS);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return entries;
    return entries.filter((e) => {
      const blob = [
        e.action,
        e.actorSubject,
        e.actorAudience,
        e.resourceName,
        e.requestId,
        e.sourceIp,
        e.errorMessage,
        e.relayedBy,
        e.userAgent,
      ]
        .join(" ")
        .toLowerCase();
      return blob.includes(q);
    });
  }, [entries, search]);

  return (
    <TableScope id={TABLE_ID} sort={sort}>
      <ListPage
        header={
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h2 className="text-lg font-semibold">Audit log</h2>
              <p className={cn(T.helper, "max-w-prose")}>
                Append-only mutation log filtered to this tenant. Cross-tenant
                view is at{" "}
                <Link
                  href="/audit"
                  className="text-primary hover:underline font-mono"
                >
                  /audit
                </Link>{" "}
                (platform-admin oversight).
              </p>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void refresh()}
              disabled={loading}
            >
              <ArrowPathIcon
                className={cn("size-4", loading && "animate-spin")}
              />
              Refresh
            </Button>
          </div>
        }
        toolbar={
          <>
            <TableToolbar
              query={search}
              onQueryChange={setSearch}
              searchLabel="Filter the audit log"
              placeholder="Filter by action, actor, resource, client, request id…"
              facets={facetControls}
            >
              <span className={T.hint}>
                {entries.length} loaded
                {nextCursor ? " · more available" : ""}
              </span>
            </TableToolbar>

            {error && (
              <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                <ExclamationTriangleIcon className="mt-0.5 size-4 shrink-0" />
                <span>{error}</span>
              </div>
            )}
          </>
        }
        footer={
          nextCursor && (
            <div className="flex justify-center">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void loadMore()}
                disabled={loading}
              >
                Load more
              </Button>
            </div>
          )
        }
      >
        <AuditLogTable
          entries={filtered}
          loading={loading}
          filtered={!!search}
          sort={sort}
          onSort={toggleSort}
          onOpen={(id) => detail.open(id)}
          rowProps={detail.rowProps}
        />
      </ListPage>

      <AuditEntryPanel
        entryId={detail.openId}
        tab={detail.tab}
        onTabChange={detail.setTab}
        onClose={detail.close}
      />
    </TableScope>
  );
}
