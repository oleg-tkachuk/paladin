"use client";

import Link from "next/link";
import {
  AdjustmentsVerticalIcon,
  ArrowUpTrayIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  ShieldCheckIcon,
  UserCircleIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { DashboardWidgets } from "@/components/DashboardWidgets";
import { DispatcherStatsCard } from "@/components/DispatcherStatsCard";
import { Card, CardContent } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { useStats } from "@/context/StatsContext";
import { useAuth } from "@/context/AuthContext";
import {
  type ComponentHealth,
  componentStatusLabel,
} from "@/lib/connect/system";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { uiBuildInfo, type BuildInfo } from "@/lib/ui/build-info";

// /  — Dashboard.
//
// A status board, not a launcher. Navigation is the sidebar's job and
// Cmd+K's; this page answers one question — "is anything wrong, and what
// happened recently?" — with three attention widgets (recent activity,
// failed operations, budget alerts), the event-dispatcher backlog, and a
// slim footer strip carrying the health rollup, signed-in identity, and
// the backend/UI build pair so a version skew is obvious at a glance.

// BuildBadge renders one "label v · sha" pill for the footer's backend /
// UI pair — stacked so a skew (stale tab vs redeployed backend) shows.
function BuildBadge({ label, build }: { label: string; build: BuildInfo }) {
  if (!build.version) return null;
  return (
    <div className="flex items-center gap-2">
      <AdjustmentsVerticalIcon className="size-4 shrink-0" />
      <span>
        <span className="text-muted-foreground">{label}</span>{" "}
        <span className={cn(T.code, "text-foreground")}>{build.version}</span>
        {build.commit && (
          <>
            {" · "}
            <span
              className={cn(T.code, "text-foreground")}
              title={build.buildTime || undefined}
            >
              {build.commit}
            </span>
          </>
        )}
      </span>
    </div>
  );
}

// backendBuild lifts the *VersionInfo shape SystemService returns onto
// the BuildInfo shape BuildBadge consumes. Empty shell until stats land.
function backendBuild(
  stats: { version?: { version: string; commit: string } } | null | undefined,
): BuildInfo {
  return {
    version: stats?.version?.version ?? "",
    commit: (stats?.version?.commit ?? "").slice(0, 7),
    buildTime: "",
  };
}

export default function DashboardPage() {
  const { user } = useAuth();
  const { stats } = useStats();
  // UI build-info is bundle-time static — three baked-in env lookups.
  const ui = uiBuildInfo();

  // ── Health rollup ──────────────────────────────────────────────────
  const components: ComponentHealth[] = stats?.health?.components ?? [];
  const rollupKey =
    stats?.health?.status !== undefined
      ? componentStatusLabel(stats.health.status).toUpperCase()
      : "UNKNOWN";
  const rollupMeta =
    rollupKey === "OK" || rollupKey === "HEALTHY"
      ? {
          label: "Healthy",
          color: "text-success",
          dot: "bg-success",
          Icon: CheckCircleIcon,
        }
      : rollupKey === "DEGRADED"
        ? {
            label: "Degraded",
            color: "text-warning",
            dot: "bg-warning",
            Icon: ExclamationTriangleIcon,
          }
        : rollupKey === "UNHEALTHY" ||
            rollupKey === "ERROR" ||
            rollupKey === "DOWN"
          ? {
              label: "Unhealthy",
              color: "text-destructive",
              dot: "bg-destructive",
              Icon: XCircleIcon,
            }
          : {
              label: "Unknown",
              color: "text-muted-foreground",
              dot: "bg-muted-foreground",
              Icon: ShieldCheckIcon,
            };
  const RollupIcon = rollupMeta.Icon;

  return (
    <div className="space-y-8">
      <PageHeader
        title="Dashboard"
        description="System health, recent activity, and anything that needs attention."
        actions={
          <Button size="sm" asChild>
            <Link href="/upload">
              <ArrowUpTrayIcon className="size-4" />
              Upload
            </Link>
          </Button>
        }
      />

      {/* ─── Attention widgets: recent activity, failed ops, budgets ── */}
      <DashboardWidgets />

      {/* ─── Event-dispatcher backlog (SystemService.GetDispatcherStats) */}
      <DispatcherStatsCard />

      {/* ─── Footer strip: health rollup · identity · build pair ────── */}
      <Card>
        <CardContent className="flex flex-wrap items-center gap-x-6 gap-y-3 px-5 py-4">
          <div className={cn(T.pill, rollupMeta.color)}>
            <span
              className={cn(
                T.pillDot,
                rollupMeta.dot,
                rollupMeta.label !== "Healthy" &&
                  rollupMeta.label !== "Unknown" &&
                  "animate-pulse",
              )}
            />
            <RollupIcon className="size-4" />
            <span>{rollupMeta.label}</span>
            <Badge variant="outline" className={cn(T.labelTight, "font-mono")}>
              {components.length} comp
            </Badge>
          </div>
          <div className={cn(T.hint, "flex items-center gap-2")}>
            <UserCircleIcon className="size-4" />
            <span>
              Signed in as{" "}
              <span className="font-medium text-foreground">
                {user?.displayName || user?.subject || "—"}
              </span>
              {user?.tenantId && (
                <>
                  {" · "}
                  <Link
                    href="/tenants"
                    className={cn(
                      T.code,
                      "text-foreground hover:text-primary hover:underline",
                    )}
                  >
                    {user.tenantId.slice(0, 8)}…{user.tenantId.slice(-4)}
                  </Link>
                </>
              )}
            </span>
          </div>
          <div
            className={cn(T.hint, "ml-auto flex flex-col items-end gap-0.5")}
          >
            <BuildBadge label="backend" build={backendBuild(stats)} />
            <BuildBadge label="ui" build={ui} />
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
