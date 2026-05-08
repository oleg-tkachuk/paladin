"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  ArrowPathIcon,
  ClipboardDocumentListIcon,
  ExclamationTriangleIcon,
  MagnifyingGlassIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useAuditLogs } from "@/hooks/useAuditLogs";

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

// /audit — read-only view of admin/v1.AuditLogService.ListAuditLog.
//
// The previous page targeted a legacy HTTP-style payload (method/path/
// httpStatus/logType) that the backend stopped emitting; the table has
// been rebuilt around the live AuditLogEntry shape (action / actor /
// resource / error). The free-text search box filters client-side over
// the loaded page; bigger filtering moves to the CEL `filter` arg in a
// follow-up if it becomes useful.

function formatTimestamp(ts: { seconds: bigint } | undefined): string {
  if (!ts) return "—";
  const ms = Number(ts.seconds) * 1000;
  if (!ms) return "—";
  try {
    return new Date(ms).toISOString().replace("T", " ").replace(".000Z", "Z");
  } catch {
    return "—";
  }
}

function actionPalette(
  action: string,
  hasError: boolean,
): "destructive" | "warning" | "info" | "success" | "outline" {
  if (hasError) return "destructive";
  const a = action.toLowerCase();
  if (a.includes("delete") || a.includes("revoke")) return "destructive";
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
        }
      />

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[240px]">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Filter by action, actor, resource, request id…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <span className="text-xs text-muted-foreground">
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
              <TableHead className="w-[180px]">When</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Actor</TableHead>
              <TableHead className="hidden lg:table-cell">Resource</TableHead>
              <TableHead className="hidden xl:table-cell w-[180px]">
                Request
              </TableHead>
              <TableHead className="hidden 2xl:table-cell w-[140px]">
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
                    className={cn(hasError && "bg-destructive/5")}
                  >
                    <TableCell className="font-mono text-[11px] text-muted-foreground">
                      {formatTimestamp(e.at)}
                    </TableCell>
                    <TableCell>
                      <div className="space-y-1">
                        <Badge
                          variant={actionPalette(e.action, hasError)}
                          className="font-mono text-[11px]"
                        >
                          {e.action || "(unknown)"}
                        </Badge>
                        {hasError && (
                          <div className="flex items-start gap-1.5 text-[11px] text-destructive">
                            <ExclamationTriangleIcon className="mt-0.5 size-3 shrink-0" />
                            <span className="line-clamp-2">
                              {e.errorMessage}
                            </span>
                          </div>
                        )}
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="space-y-0.5">
                        <span className="text-sm">
                          {e.actorSubject || (
                            <span className="italic text-muted-foreground">
                              system
                            </span>
                          )}
                        </span>
                        {e.actorAudience && (
                          <div className="font-mono text-[10px] text-muted-foreground">
                            aud {e.actorAudience}
                          </div>
                        )}
                      </div>
                    </TableCell>
                    <TableCell className="hidden lg:table-cell font-mono text-[11px] text-muted-foreground">
                      {e.resourceName || "—"}
                    </TableCell>
                    <TableCell className="hidden xl:table-cell">
                      {e.requestId ? (
                        <span
                          className="font-mono text-[11px] text-muted-foreground"
                          title={e.requestId}
                        >
                          {e.requestId.slice(0, 12)}
                          {e.requestId.length > 12 ? "…" : ""}
                        </span>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                      {e.sourceIp && (
                        <div className="font-mono text-[10px] text-muted-foreground">
                          {e.sourceIp}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="hidden 2xl:table-cell">
                      {e.capabilityId ? (
                        // Cross-link: clicking jumps to /capabilities
                        // and pre-fills the cap_id filter on that
                        // page (the page reads ?id=… on mount).
                        // Truncated display so the column stays
                        // narrow; full id in title for hover.
                        <Link
                          href={`/capabilities?id=${e.capabilityId}`}
                          className="font-mono text-[11px] text-primary hover:underline"
                          title={e.capabilityId}
                        >
                          {e.capabilityId.slice(0, 8)}…
                        </Link>
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
