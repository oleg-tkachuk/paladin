"use client";

// DashboardWidgets — the dashboard's "attention row". Three compact
// cards answering "is anything wrong right now?":
//
//   1. Recent activity (last 5 audit entries platform-wide)
//   2. Failed operations (background-ops drawer's "failed" subset
//      inlined for visibility — operators noticed the drawer-only
//      placement hid red signals)
//   3. Tenants approaching budget cap (>= 80% spend)
//
// Each widget fetches independently and degrades silently. The home
// page composes this as its lead row via:
//   <DashboardWidgets />
//
// Server load is minimal: small page sizes, no polling — only
// re-fetch on tenant context refresh from RefreshContext.

import { useMemo } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowRightIcon,
  BoltIcon,
  ClipboardDocumentListIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import {
  auditClient,
  adminOperationClient,
  tenantBudgetClient,
} from "@/lib/connect/client";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import type { TenantBudgetSummary } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import { formatMoney } from "@/lib/format/money";

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
  // TanStack passes an AbortSignal to the queryFn and cancels the RPC on
  // unmount / key-change without logging an AbortError. On failure `data`
  // is undefined → rows defaults to [] and the widget degrades silently.
  const { data: rows = [], isLoading: loading } = useQuery<AuditLogEntry[]>({
    queryKey: ["dashboard", "recentActivity"],
    queryFn: ({ signal }) =>
      auditClient
        .listAuditLog(
          { page: { pageSize: 5, pageToken: "" }, filter: "" },
          { signal },
        )
        .then((res) => res.entries),
  });
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
  const { data: ops = [], isLoading: loading } = useQuery<Operation[]>({
    queryKey: ["dashboard", "failedOps"],
    queryFn: ({ signal }) =>
      adminOperationClient
        .listOperations(
          {
            page: { pageSize: 5, pageToken: "" },
            filter: "error_message != null",
          },
          { signal },
        )
        .then((res) => res.operations.filter((o) => !!opError(o)).slice(0, 5)),
  });
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

// Threshold for the "approaching cap" surface. 80 % is the
// platform-wide default — operators escalate to a deeper view
// (/billing or the tenant's Budget tab) for fine-grained action.
const BUDGET_ALERT_THRESHOLD = 80;
const BUDGET_ALERT_LIMIT = 5;

function BudgetAlertsWidget() {
  const {
    data: rows = [],
    isLoading: loading,
    error,
  } = useQuery<TenantBudgetSummary[]>({
    queryKey: ["dashboard", "budgetAlerts"],
    queryFn: ({ signal }) =>
      tenantBudgetClient
        .summarize(
          {
            thresholdPct: BUDGET_ALERT_THRESHOLD,
            unlimitedOnly: false,
            excludeInactive: true,
            limit: BUDGET_ALERT_LIMIT,
          },
          { signal },
        )
        .then((res) => res.summaries),
  });

  const overCap = useMemo(
    () => rows.filter((r) => r.utilisationPct >= 100).length,
    [rows],
  );

  return (
    <Card
      className={cn(
        overCap > 0 && "border-destructive/40 bg-destructive/[0.02]",
      )}
    >
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <BoltIcon
            className={cn(
              "size-4",
              overCap > 0 ? "text-destructive" : "text-muted-foreground",
            )}
          />
          Budget alerts
          {rows.length > 0 && (
            <Badge
              variant={overCap > 0 ? "destructive" : "secondary"}
              className={T.labelTight}
            >
              {rows.length} ≥{BUDGET_ALERT_THRESHOLD}%
            </Badge>
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        {loading ? (
          <Skeleton className="h-16 w-full" />
        ) : error ? (
          <p className="py-2 text-center text-xs text-muted-foreground">
            Summary unavailable — capability subsystem may be disabled.
          </p>
        ) : rows.length === 0 ? (
          <p className="py-4 text-center text-xs text-muted-foreground">
            No tenants are approaching their cap.
          </p>
        ) : (
          <ul className="space-y-1.5">
            {rows.map((r) => (
              <BudgetAlertRow key={r.tenantId} row={r} />
            ))}
          </ul>
        )}
        <Link
          href="/billing"
          className="mt-3 inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
        >
          Open billing <ArrowRightIcon className="size-3" />
        </Link>
      </CardContent>
    </Card>
  );
}

function BudgetAlertRow({ row }: { row: TenantBudgetSummary }) {
  const pct = Math.round(row.utilisationPct);
  const over = pct >= 100;
  const spent = row.budget?.spentAmount ?? 0;
  const cap = row.budget?.maxBudgetAmount ?? 0;
  const unit = row.budget?.unitCode || "UNIT";
  const handle = row.slug || row.tenantId;
  return (
    <li className="text-xs">
      <Link
        href={`/tenants/${encodeURIComponent(handle)}/budget`}
        className="group block rounded px-1.5 py-1 hover:bg-muted/40"
      >
        <div className="flex items-baseline justify-between gap-2">
          <span className="truncate font-medium group-hover:underline">
            {row.displayName || handle}
          </span>
          <span
            className={cn(
              "shrink-0 font-mono tabular-nums",
              over
                ? "text-destructive font-semibold"
                : pct >= 90
                  ? "text-amber-600 dark:text-amber-400"
                  : "text-muted-foreground",
            )}
          >
            {pct}%
          </span>
        </div>
        {/* Linear progress bar; clamped so 120%-overruns still render
            visually distinct without overflowing the row. */}
        <div className="mt-1 h-1 w-full overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              "h-full rounded-full",
              over
                ? "bg-destructive"
                : pct >= 90
                  ? "bg-amber-500"
                  : "bg-chart-2",
            )}
            style={{ width: `${Math.min(100, pct)}%` }}
          />
        </div>
        <p className="mt-0.5 text-[10px] text-muted-foreground">
          {formatMoney(spent, unit)} / {formatMoney(cap, unit)}
        </p>
      </Link>
    </li>
  );
}
