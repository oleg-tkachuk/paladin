"use client";

import { ListPage } from "@/components/layout/templates/ListPage";
import { useDetailPanel } from "@/hooks/useDetailPanel";
import { useCallback, useRef, useState } from "react";
import { useDebouncedValue } from "@/hooks/useDebouncedValue";
import {
  ArrowDownTrayIcon,
  ArrowPathIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { LiveToggle } from "@/components/ui/LiveToggle";
import { liveState } from "@/lib/live";
import { PageHeader } from "@/components/layout/PageHeader";
import { useAuditLogs } from "@/hooks/useAuditLogs";
import { searchFilter } from "@/lib/cel";
import { useAuditStream } from "@/hooks/useAuditStream";
import { ExportAuditLogDialog } from "./ExportAuditLogDialog";

import { Button } from "@/components/ui/button";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { AUDIT_ORDER_FIELDS, type AuditOrderField } from "@/lib/auditOrder";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { TableScope } from "@/components/ui/TableScope";
import { AuditLogTable } from "@/components/features/audit/AuditLogTable";
import {
  AUDIT_ENTRY_TABS,
  AuditEntryPanel,
} from "@/components/features/audit/AuditEntryPanel";

/** This list's id in the user's table preferences. */
const TABLE_ID = "audit";

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

export default function AuditPage() {
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  const debouncedSearch = useDebouncedValue(search);
  // Sorted on the server: the log loads a page at a time, so a sort in the
  // browser would order only what has loaded. A new sort starts over.
  const { sort, toggleSort } = useTableSort<AuditOrderField>(undefined, {
    url: PAGE_TABLE,
    tableId: TABLE_ID,
    columns: AUDIT_ORDER_FIELDS,
  });
  const { entries, loading, error, nextCursor, refresh, loadMore } =
    useAuditLogs(AUDIT_PAGE_SIZE, searchFilter(debouncedSearch), "", sort);

  // Live feed — SSE events are an invalidation signal, not a data source:
  // each push triggers a trailing-debounced refresh() so a burst of audit
  // rows costs one ListAuditLog, and the full entry (incl. before/after)
  // flows through the same RPC path as a manual refresh.
  // Row-click detail (GetAuditLogEntry by id) + header-triggered export.
  // The open entry and its tab are in the URL, so an open entry's address
  // links to it; it opens beside the log, which stays where it was.
  const detail = useDetailPanel(AUDIT_ENTRY_TABS);
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

  return (
    <TableScope id={TABLE_ID} sort={sort}>
      <ListPage
        header={
          <PageHeader
            title="Audit Logs"
            description="Append-only log of every mutation served by the control plane."
            showDefaultActions={false}
            actions={
              <>
                <LiveToggle
                  state={liveState({ live, connected })}
                  onToggle={() => setLive((v) => !v)}
                />
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
        }
        toolbar={
          <>
            <TableToolbar
              query={search}
              onQueryChange={setSearch}
              searchLabel="Filter the audit log"
              placeholder="Filter by action, actor, resource, client, request id…"
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
          entries={entries}
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
      <ExportAuditLogDialog open={exportOpen} onOpenChange={setExportOpen} />
    </TableScope>
  );
}
