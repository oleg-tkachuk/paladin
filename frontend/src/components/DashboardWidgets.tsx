"use client";

// DashboardWidgets — drop-in row for the home page. Three compact
// cards over the existing tile grid:
//
//   1. Recent activity (last 5 audit entries platform-wide)
//   2. Failed operations (background-ops drawer's "failed" subset
//      inlined for visibility — operators noticed the drawer-only
//      placement hid red signals)
//   3. Tenants approaching budget cap (>= 80% spend)
//
// Each widget fetches independently and degrades silently. The home
// page composes this above its tile grid via:
//   <DashboardWidgets />
//
// Server load is minimal: small page sizes, no polling — only
// re-fetch on tenant context refresh from RefreshContext.

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { ConnectError } from "@connectrpc/connect";
import {
  ArrowRightIcon,
  BoltIcon,
  ClipboardDocumentListIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { auditClient, adminOperationClient } from "@/lib/connect/client";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";

// Operation.result is a oneof — case "error" carries google.rpc.Status.
function opError(o: Operation): string {
  if (o.result.case === "error") return o.result.value.message || "error";
  return "";
}
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { Badge } from "@/components/ui/badge";
import { RelativeTime } from "@/components/RelativeTime";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

export function DashboardWidgets() {
  return (
    <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
      <RecentActivityWidget />
      <FailedOpsWidget />
      <BudgetAlertsWidget />
    </div>
  );
}

function RecentActivityWidget() {
  const [rows, setRows] = useState<AuditLogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await auditClient.listAuditLog({
          page: { pageSize: 5, pageToken: "" },
          filter: "",
        });
        if (!cancelled) setRows(res.entries);
      } catch {
        // silent — widget hides itself
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);
  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-sm">Recent activity</CardTitle>
          <Link
            href="/audit"
            className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
          >
            See all <ArrowRightIcon className="size-3" />
          </Link>
        </div>
      </CardHeader>
      <CardContent className="pt-0">
        {loading ? (
          <div className="space-y-2">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-6 w-full" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <p className="py-4 text-center text-xs text-muted-foreground">
            No recent activity.
          </p>
        ) : (
          <ul className="space-y-1.5">
            {rows.map((e) => {
              const action = (e.action || "").split("/").pop() || e.action;
              return (
                <li key={e.entryId} className="flex items-center gap-2 text-xs">
                  <ClipboardDocumentListIcon
                    className={cn(
                      "size-3.5 shrink-0",
                      e.errorMessage
                        ? "text-destructive"
                        : "text-muted-foreground",
                    )}
                  />
                  <span className="font-medium">{action}</span>
                  <span className="truncate text-muted-foreground">
                    {e.actorSubject}
                  </span>
                  <span className="ml-auto whitespace-nowrap text-[10px] text-muted-foreground">
                    <RelativeTime ts={e.at} />
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function FailedOpsWidget() {
  const [ops, setOps] = useState<Operation[]>([]);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await adminOperationClient.listOperations({
          page: { pageSize: 5, pageToken: "" },
          filter: "error_message != null",
        });
        if (!cancelled)
          setOps(res.operations.filter((o) => !!opError(o)).slice(0, 5));
      } catch {
        /* silent */
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);
  return (
    <Card
      className={cn(
        ops.length > 0 && "border-destructive/40 bg-destructive/[0.02]",
      )}
    >
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <ExclamationTriangleIcon
            className={cn(
              "size-4",
              ops.length > 0 ? "text-destructive" : "text-muted-foreground",
            )}
          />
          Failed operations
          {ops.length > 0 && (
            <Badge variant="destructive" className={T.labelTight}>
              {ops.length}
            </Badge>
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        {loading ? (
          <Skeleton className="h-12 w-full" />
        ) : ops.length === 0 ? (
          <p className="py-4 text-center text-xs text-muted-foreground">
            No failures in the recent window.
          </p>
        ) : (
          <ul className="space-y-1.5">
            {ops.map((o) => (
              <li key={o.name} className="text-xs">
                <p className="font-medium">{o.type || o.name}</p>
                <p className="truncate font-mono text-[10px] text-destructive">
                  {opError(o)}
                </p>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function BudgetAlertsWidget() {
  // Budget queries are per-tenant; this widget is intentionally a
  // placeholder until a cross-tenant budget summary RPC exists
  // (BACKLOG). Renders the "no alerts" empty state so dashboard
  // layout stays predictable.
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <BoltIcon className="size-4 text-muted-foreground" />
          Budget alerts
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        <p className="py-4 text-center text-xs text-muted-foreground">
          Cross-tenant budget summary RPC pending — see BACKLOG.
        </p>
        <Link
          href="/billing"
          className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
        >
          Open billing <ArrowRightIcon className="size-3" />
        </Link>
      </CardContent>
    </Card>
  );
}
