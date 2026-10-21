"use client";

import Link from "next/link";
import { ClipboardDocumentListIcon } from "@heroicons/react/24/outline";

import { ListBody } from "@/components/layout/templates/ListPage";
import { openableRow } from "@/components/ui/RecordDialog";
import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import {
  SHOWN_IN_CARD,
  Table,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Timestamp } from "@/components/Timestamp";
import {
  CapabilityHoverCard,
  capabilityHref,
} from "@/components/features/hover/ResourceHoverCards";
import type { AuditLogEntry } from "@/hooks/useAuditLogs";
import type { SortState } from "@/hooks/useTableSort";
import { AUDIT_ORDER, type AuditOrderField } from "@/lib/auditOrder";
import { emptyReason } from "@/lib/emptyState";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { ActorName } from "./ActorName";
import { AuditActionCell } from "./AuditActionCell";
import { ClientCell } from "./ClientCell";
import { RelayedBy } from "./RelayedBy";
import { resourceInTenant } from "./resource";

/** The columns a full-width row spans, hidden ones included. */
const COLUMNS = 7;
/** How much of a request id a row shows; the rest is in the tooltip. */
const SHORT_REQUEST_ID = 8;
/** How much of a capability id a row shows. */
const SHORT_CAPABILITY_ID = 8;

const DASH = <span className="text-muted-foreground">—</span>;

/**
 * The audit log as a table, for the cross-tenant log and a tenant's alike.
 *
 * Every identifier here is long and unbroken — `apikey:<uuid>`, resource
 * names, request ids — so a cell shows what a reader scans for on one line and
 * keeps the whole value in its tooltip; the entry's detail has all of it.
 */
export function AuditLogTable({
  entries,
  loading,
  filtered,
  sort,
  onSort,
  onOpen,
  rowProps,
}: {
  entries: AuditLogEntry[];
  /** The first page is in flight and there is nothing to show yet. */
  loading: boolean;
  /** A filter is applied, so an empty table says so. */
  filtered: boolean;
  sort: SortState<AuditOrderField>;
  onSort: (column: AuditOrderField) => void;
  onOpen: (entryId: string) => void;
  /** What marks an entry's row as the open one. */
  rowProps: (entryId: string) => Record<string, unknown>;
}) {
  const sortProps = { current: sort, onSort };
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <SortableHead
            label="When"
            column={AUDIT_ORDER.at}
            className="@4xl:w-44"
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
            label="Client"
            column={AUDIT_ORDER.client}
            className="hidden @3xl:table-cell"
            {...sortProps}
          />
          <SortableHead
            label="Resource"
            column={AUDIT_ORDER.resource}
            className="hidden @2xl:table-cell"
            {...sortProps}
          />
          <StaticHead className="hidden @5xl:table-cell">Request</StaticHead>
          <StaticHead className="hidden @6xl:table-cell">Capability</StaticHead>
        </TableRow>
      </TableHeader>
      <ListBody
        colSpan={COLUMNS}
        loading={loading && entries.length === 0}
        // A failed read is the banner above the table.
        error={null}
        empty={
          entries.length === 0
            ? {
                icon: ClipboardDocumentListIcon,
                what: "audit entries",
                reason: emptyReason({ filtered }),
              }
            : null
        }
      >
        {entries.map((e) => {
          const row = openableRow(() => onOpen(e.entryId));
          return (
            <TableRow
              key={e.entryId}
              {...row}
              {...rowProps(e.entryId)}
              className={cn(
                row.className,
                "align-top",
                e.errorMessage && "bg-destructive/5",
              )}
            >
              {/* Wrapped where the table is narrow, so the date does not
                  push the actor out of the card. */}
              <TableCell
                className={cn(
                  T.codeSmall,
                  "whitespace-normal text-muted-foreground @4xl:whitespace-nowrap",
                )}
              >
                <Timestamp ts={e.at} />
              </TableCell>
              <TableCell>
                <div className="max-w-40 @4xl:max-w-56">
                  <AuditActionCell
                    action={e.action}
                    errorMessage={e.errorMessage}
                  />
                </div>
              </TableCell>
              <TableCell>
                <div className="min-w-0 max-w-40 space-y-0.5 @4xl:max-w-56">
                  <div className={cn(T.body, "truncate")}>
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
                  </div>
                  {e.actorAudience && (
                    <div className={cn(T.hint, "truncate")}>
                      {e.actorAudience}
                    </div>
                  )}
                  <RelayedBy client={e.relayedBy} />
                </div>
              </TableCell>
              <TableCell
                className={cn("hidden @3xl:table-cell", SHOWN_IN_CARD)}
              >
                <ClientCell userAgent={e.userAgent} />
              </TableCell>
              {/* The one column that gives: it takes what the others leave and
                  truncates there, so no width overflows the card. */}
              {/* On a phone's card the row has room again, so it shows there
                  in full width rather than as the column that gives. */}
              <TableCell
                className={cn(
                  "hidden w-full max-w-0 @2xl:table-cell",
                  SHOWN_IN_CARD,
                  "@max-md:max-w-none",
                )}
              >
                {e.resourceName ? (
                  <div
                    className={cn(T.code, "truncate text-muted-foreground")}
                    title={e.resourceName}
                  >
                    {resourceInTenant(e.resourceName)}
                  </div>
                ) : (
                  DASH
                )}
              </TableCell>
              <TableCell className="hidden @5xl:table-cell">
                <div className="space-y-0.5">
                  {e.requestId ? (
                    <div
                      className={cn(T.code, "text-muted-foreground")}
                      title={e.requestId}
                    >
                      {e.requestId.slice(0, SHORT_REQUEST_ID)}
                    </div>
                  ) : (
                    DASH
                  )}
                  {e.sourceIp && (
                    <div className={cn(T.codeSmall, "text-muted-foreground")}>
                      {e.sourceIp}
                    </div>
                  )}
                </div>
              </TableCell>
              <TableCell className="hidden @6xl:table-cell">
                {!e.capabilityId ? (
                  DASH
                ) : e.actorTenantId ? (
                  <CapabilityHoverCard
                    id={e.capabilityId}
                    tenantId={e.actorTenantId}
                  >
                    <Link
                      href={capabilityHref(e.actorTenantId, e.capabilityId)}
                      className={cn(T.code, "text-primary hover:underline")}
                      title={e.capabilityId}
                      onClick={(ev) => ev.stopPropagation()}
                    >
                      {e.capabilityId.slice(0, SHORT_CAPABILITY_ID)}
                    </Link>
                  </CapabilityHoverCard>
                ) : (
                  // A system action has no tenant to open it under.
                  <span
                    className={cn(T.code, "text-muted-foreground")}
                    title={e.capabilityId}
                  >
                    {e.capabilityId.slice(0, SHORT_CAPABILITY_ID)}
                  </span>
                )}
              </TableCell>
            </TableRow>
          );
        })}
      </ListBody>
    </Table>
  );
}
