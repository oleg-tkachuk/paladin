"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  ArrowDownTrayIcon,
  ArrowPathIcon,
  ClipboardDocumentListIcon,
  ExclamationTriangleIcon,
  MagnifyingGlassIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useAuditLogs } from "@/hooks/useAuditLogs";
import { useAuditStream } from "@/hooks/useAuditStream";
import { AuditEntryDetailDialog } from "./AuditEntryDetailDialog";
import { ExportAuditLogDialog } from "./ExportAuditLogDialog";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
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
import { ActorName } from "@/components/features/audit/ActorName";
import { Timestamp } from "@/components/Timestamp";

// /audit — read-only view of admin/v1.AuditLogService.ListAuditLog.
//
// The previous page targeted a legacy HTTP-style payload (method/path/
// httpStatus/logType) that the backend stopped emitting; the table has
// been rebuilt around the live AuditLogEntry shape (action / actor /
// resource / error). The free-text search box filters client-side over
// the loaded page; bigger filtering moves to the CEL `filter` arg in a
// follow-up if it becomes useful.

function actionPalette(
  action: string,
  hasError: boolean,
): "destructive" | "warning" | "info" | "success" | "outline" {
  if (hasError) return "destructive";
  const a = action.toLowerCase();
  // Security events (e.g. refresh-token reuse) stand out even without an error.
  if (a.includes("delete") || a.includes("revoke") || a.includes("reuse"))
    return "destructive";
  if (a.includes("update") || a.includes("rotate") || a.includes("set"))
    return "warning";
  if (a.includes("create") || a.includes("login")) return "success";
  if (a.includes("get") || a.includes("list") || a.includes("read"))
    return "info";
  return "outline";
}

export default function AuditPage() {
  const { entries, loading, error, nextCursor, refresh, loadMore } =
    useAuditLogs(100, "");

  // Optional ?audience=… deep-link (used by /mcp → audit) — preset
  // the search box on first mount via the lazy useState initialiser.
  // Chip stays editable; clearing the search input clears the
  // filter.
  const params = useSearchParams();
  const [search, setSearch] = useState(() => params?.get("audience") ?? "");

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

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-60">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Filter by action, actor, resource, request id…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <span className={T.hint}>
          {entries.length} loaded
          {nextCursor ? " · more available" : ""}
        </span>
      </div>

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
              <TableHead className="@md:w-45">When</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Actor</TableHead>
              <TableHead className="hidden @2xl:table-cell">Resource</TableHead>
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
                return (
                  <TableRow
                    key={e.entryId}
                    className={cn(
                      "cursor-pointer hover:bg-muted/40",
                      hasError && "bg-destructive/5",
                    )}
                    onClick={() => setDetailId(e.entryId)}
                    tabIndex={0}
                    onKeyDown={(ev) => {
                      if (ev.key === "Enter" || ev.key === " ") {
                        ev.preventDefault();
                        setDetailId(e.entryId);
                      }
                    }}
                  >
                    <TableCell
                      className={cn(
                        T.codeSmall,
                        "whitespace-normal text-muted-foreground @md:whitespace-nowrap",
                      )}
                    >
                      <Timestamp ts={e.at} />
                    </TableCell>
                    {/* Action, actor and resource are long unbroken
                        identifiers (RPC paths, `apikey:<uuid>`, resource
                        names): they wrap, or one row widens the table past
                        its card. */}
                    <TableCell className="max-w-80 whitespace-normal">
                      <div className="space-y-1">
                        <Badge
                          variant={actionPalette(e.action, hasError)}
                          className={cn(
                            T.code,
                            "h-auto max-w-full whitespace-normal break-all",
                          )}
                        >
                          {e.action || "(unknown)"}
                        </Badge>
                        {hasError && (
                          <div className="flex items-start gap-1.5 text-xs text-destructive">
                            <ExclamationTriangleIcon className="mt-0.5 size-3 shrink-0" />
                            <span className="line-clamp-2">
                              {e.errorMessage}
                            </span>
                          </div>
                        )}
                      </div>
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
