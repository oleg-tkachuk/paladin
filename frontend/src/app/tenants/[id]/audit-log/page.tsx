"use client";

// Tenant-scoped audit log. Same table shape as the cross-tenant
// /audit page; the difference is the tenant's trail, which the server
// selects (ListAuditLogRequest.tenant_id): entries the tenant's
// principals made, and entries on its resources — a platform admin's
// work inside the tenant included.
//
// Free-text search filters client-side over the loaded page, same
// affordance as /audit.

import { EmptyTableRow } from "@/components/ui/EmptyState";
import { emptyReason } from "@/lib/emptyState";
import { useMemo } from "react";
import Link from "next/link";
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
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
import {
  ArrowPathIcon,
  ClipboardDocumentListIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { useAuditLogs } from "@/hooks/useAuditLogs";

import { Button } from "@/components/ui/button";
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
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../tenant-context";
import { ActorName } from "@/components/features/audit/ActorName";
import { RelayedBy } from "@/components/features/audit/RelayedBy";
import { AuditActionCell } from "@/components/features/audit/AuditActionCell";
import { Timestamp } from "@/components/Timestamp";

/** Entries per page of the tenant's trail. */
const TENANT_AUDIT_PAGE_SIZE = 100;

export default function TenantAuditLogPage() {
  const tenant = useTenant();

  // Sorted on the server: the log loads a page at a time, so a sort in the
  // browser would order only what has loaded. A new sort starts over.
  const { sort, toggleSort } = useTableSort<AuditOrderField>(undefined, {
    url: PAGE_TABLE,
    columns: AUDIT_ORDER_FIELDS,
  });
  const sortProps = { current: sort, onSort: toggleSort };
  const { entries, loading, error, nextCursor, refresh, loadMore } =
    useAuditLogs(TENANT_AUDIT_PAGE_SIZE, "", tenant.tenantId, sort);

  const [search, setSearch] = useTableQuery(PAGE_TABLE);

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
      ]
        .join(" ")
        .toLowerCase();
      return blob.includes(q);
    });
  }, [entries, search]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <h2 className="text-lg font-semibold">Audit log</h2>
          <p className={cn(T.helper, "max-w-prose")}>
            Append-only mutation log filtered to this tenant. Cross-tenant view
            is at{" "}
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
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
          Refresh
        </Button>
      </div>

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
              <EmptyTableRow
                colSpan={6}
                icon={ClipboardDocumentListIcon}
                what="audit entries"
                reason={emptyReason({ filtered: !!search })}
              />
            ) : (
              filtered.map((e) => {
                const hasError = !!e.errorMessage;
                return (
                  <TableRow
                    key={e.entryId}
                    className={cn(hasError && "bg-destructive/5")}
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
                        // Copyable, not a link: this went to /capabilities,
                        // a page that does not exist, and the tenant's
                        // capability page cannot open one capability by id.
                        <IdentifierCopy
                          value={e.capabilityId}
                          label="Capability ID"
                          className={T.code}
                        />
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
    </div>
  );
}
