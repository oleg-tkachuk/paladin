"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import {
  ArrowDownTrayIcon,
  ArrowPathIcon,
  ClipboardDocumentListIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useAuditLogs } from "@/hooks/useAuditLogs";
import { searchFilter } from "@/lib/cel";
import { useAuditStream } from "@/hooks/useAuditStream";
import { AuditEntryDetailDialog } from "./AuditEntryDetailDialog";
import { ExportAuditLogDialog } from "./ExportAuditLogDialog";

import { Button } from "@/components/ui/button";
import { SortableHead } from "@/components/ui/SortHeader";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { PAGE_TABLE } from "@/lib/table/urlState";
import {
  AUDIT_ORDER,
  AUDIT_ORDER_FIELDS,
  type AuditOrderField,
} from "@/lib/auditOrder";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { Card } from "@/components/ui/Card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/Skeleton";
import { openableRow } from "@/components/ui/RecordDialog";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { ActorName } from "@/components/features/audit/ActorName";
import { RelayedBy } from "@/components/features/audit/RelayedBy";
import { AuditActionCell } from "@/components/features/audit/AuditActionCell";
import { Timestamp } from "@/components/Timestamp";

// /audit — read-only view of admin/v1.AuditLogService.ListAuditLog.
//
// The previous page targeted a legacy HTTP-style payload (method/path/
// httpStatus/logType) that the backend stopped emitting; the table has
// been rebuilt around the live AuditLogEntry shape (action / actor /
// resource / error). The search box is the server's CEL `filter` over the
// entry's derived `search` text, applied to every page it reads, so it reaches
// entries not loaded yet; Load more pages on through the matches.

/** Entries a page of the log holds. */
const AUDIT_PAGE_SIZE = 100;
/** How long the search waits after a keystroke before asking the server. */
const SEARCH_DEBOUNCE_MS = 300;

export default function AuditPage() {
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [search]);
  // Sorted on the server: the log loads a page at a time, so a sort in the
  // browser would order only what has loaded. A new sort starts over.
  const { sort, toggleSort } = useTableSort<AuditOrderField>(undefined, {
    url: PAGE_TABLE,
    columns: AUDIT_ORDER_FIELDS,
  });
  const sortProps = { current: sort, onSort: toggleSort };
  const { entries, loading, error, nextCursor, refresh, loadMore } =
    useAuditLogs(AUDIT_PAGE_SIZE, searchFilter(debouncedSearch), "", sort);

  // Live feed — SSE events are an invalidation signal, not a data source:
  // each push triggers a trailing-debounced refresh() so a burst of audit
  // rows costs one ListAuditLog, and the full entry (incl. before/after)
  // flows through the same RPC path as a manual refresh.
  // Row-click detail (GetAuditLogEntry by id) + header-triggered export.
  const [detailId, setDetailId] = useState<string | null>(null);
  const [exportOpen, setExportOpen] = useState(false);

  const [live, setLive] = useState(true);
  const refreshTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onStreamEvent = useCallback(() => {
    if (refreshTimer.current) return;
    refreshTimer.current = setTimeout(() => {
      refreshTimer.current = null;
      void refresh();
    }, 800);
  }, [refresh]);
  const { connected } = useAuditStream(live, onStreamEvent);

  const filtered = entries;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Audit Logs"
        description="Append-only log of every mutation served by the control plane."
        showDefaultActions={false}
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setLive((v) => !v)}
              aria-pressed={live}
              title={
                live
                  ? connected
                    ? "Live — new entries appear automatically"
                    : "Live enabled — reconnecting…"
                  : "Live updates paused"
              }
            >
              <span
                className={cn(
                  "size-2 rounded-full",
                  live && connected && "bg-success animate-pulse",
                  live && !connected && "bg-warning",
                  !live && "bg-muted-foreground/40",
                )}
              />
              Live
            </Button>
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
            <Button
              variant="outline"
              size="sm"
              onClick={() => setExportOpen(true)}
            >
              <ArrowDownTrayIcon className="size-4" />
              Export
            </Button>
          </>
        }
      />

      <TableToolbar
        query={search}
        onQueryChange={setSearch}
        searchLabel="Filter the audit log"
        placeholder="Filter by action, actor, resource, request id…"
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

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <SortableHead
                label="When"
                column={AUDIT_ORDER.at}
                className="@md:w-45"
                {...sortProps}
              />
              <SortableHead
                label="Action"
                column={AUDIT_ORDER.action}
                {...sortProps}
              />
              <SortableHead
                label="Actor"
                column={AUDIT_ORDER.actor}
                {...sortProps}
              />
              <SortableHead
                label="Resource"
                column={AUDIT_ORDER.resource}
                className="hidden @2xl:table-cell"
                {...sortProps}
              />
              <TableHead className="hidden @4xl:table-cell w-45">
                Request
              </TableHead>
              <TableHead className="hidden @6xl:table-cell w-35">
                Capability
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && entries.length === 0 ? (
              [0, 1, 2, 3].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={6} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="h-40 text-center">
                  <div className="flex flex-col items-center gap-2 text-muted-foreground">
                    <ClipboardDocumentListIcon className="size-8 opacity-40" />
                    <p className="text-sm">
                      {search
                        ? "No entries match this filter."
                        : "No audit entries yet."}
                    </p>
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((e) => {
                const hasError = !!e.errorMessage;
                const row = openableRow(() => setDetailId(e.entryId));
                return (
                  <TableRow
                    key={e.entryId}
                    {...row}
                    className={cn(
                      row.className,
                      hasError && "bg-destructive/5",
                    )}
                  >
                    <TableCell
                      className={cn(
                        T.codeSmall,
                        "whitespace-normal text-muted-foreground @md:whitespace-nowrap",
                      )}
                    >
                      <Timestamp ts={e.at} />
                    </TableCell>
                    {/* Actor and resource are long unbroken identifiers
                        (`apikey:<uuid>`, resource names): they wrap, or one
                        row widens the table past its card. */}
                    <TableCell className="max-w-80 whitespace-normal">
                      <AuditActionCell
                        action={e.action}
                        errorMessage={e.errorMessage}
                      />
                    </TableCell>
                    <TableCell className="whitespace-normal break-all">
                      <div className="space-y-0.5">
                        <span className={T.body}>
                          {e.actorSubject ? (
                            <ActorName
                              subject={e.actorSubject}
                              tenantId={e.actorTenantId}
                            />
                          ) : (
                            <span className="italic text-muted-foreground">
                              system
                            </span>
                          )}
                        </span>
                        {e.actorAudience && (
                          <div
                            className={cn(T.codeSmall, "text-muted-foreground")}
                          >
                            aud {e.actorAudience}
                          </div>
                        )}
                        <RelayedBy client={e.relayedBy} />
                      </div>
                    </TableCell>
                    <TableCell
                      className={cn(
                        "hidden @2xl:table-cell whitespace-normal break-all",
                        T.code,
                        "text-muted-foreground",
                      )}
                    >
                      {e.resourceName || "—"}
                    </TableCell>
                    <TableCell className="hidden @4xl:table-cell">
                      {e.requestId ? (
                        <span
                          className={cn(T.code, "text-muted-foreground")}
                          title={e.requestId}
                        >
                          {e.requestId.slice(0, 12)}
                          {e.requestId.length > 12 ? "…" : ""}
                        </span>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                      {e.sourceIp && (
                        <div
                          className={cn(T.codeSmall, "text-muted-foreground")}
                        >
                          {e.sourceIp}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="hidden @6xl:table-cell">
                      {e.capabilityId ? (
                        // Capabilities are tenant-scoped now (the
                        // page lives under /tenants/<id>/capabilities).
                        // Use the audit row's actor_tenant_id to
                        // route — TenantLayout's resolver canonicalises
                        // UUID→slug on landing.
                        e.actorTenantId ? (
                          <Link
                            href={`/tenants/${encodeURIComponent(e.actorTenantId)}/capabilities?id=${e.capabilityId}`}
                            className={cn(
                              T.code,
                              "text-primary hover:underline",
                            )}
                            title={e.capabilityId}
                            onClick={(ev) => ev.stopPropagation()}
                          >
                            {e.capabilityId.slice(0, 8)}…
                          </Link>
                        ) : (
                          // No actor_tenant_id (system action): show
                          // the id as text so the column still
                          // carries the data, just not the navigation.
                          <span
                            className={cn(T.code, "text-muted-foreground")}
                            title={e.capabilityId}
                          >
                            {e.capabilityId.slice(0, 8)}…
                          </span>
                        )
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      {nextCursor && (
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
      )}

      <AuditEntryDetailDialog
        entryId={detailId}
        onOpenChange={(o) => {
          if (!o) setDetailId(null);
        }}
      />
      <ExportAuditLogDialog open={exportOpen} onOpenChange={setExportOpen} />
    </div>
  );
}
