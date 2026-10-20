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
import { FirstRunChecklist } from "@/components/dashboard/FirstRunChecklist";
import { MyOperationsWidget } from "@/components/dashboard/MyOperationsWidget";
import { QuotaAlertsWidget } from "@/components/dashboard/QuotaAlertsWidget";
import { Card, CardContent } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { StatusBar } from "@/components/ui/StatusBar";
import { useStats } from "@/context/StatsContext";
import { useAuth } from "@/context/AuthContext";
import { canUseAdminPlane } from "@/constants/roles";
import { NoAdminRole } from "@/components/NoAdminRole";
import {
  type ComponentHealth,
  componentSegments,
  componentStatusLabel,
} from "@/lib/connect/system";
import { cn } from "@/lib/utils";
import { formatCount } from "@/lib/format/locale";
import { T } from "@/lib/ui/typography";
import { uiBuildInfo, type BuildInfo } from "@/lib/ui/build-info";

// /  — Dashboard.
//
// A status board, not a launcher. Navigation is the sidebar's job and
// Cmd+K's; this page answers one question — "is anything wrong, and what
// happened recently?" — with three attention widgets (recent activity,
// failed operations, budget alerts), the event-dispatcher backlog, and a
// slim footer strip, one line high, carrying the health rollup, signed-in
// identity, and the backend/UI build pair so a version skew is obvious at a
// glance.

// BuildBadge renders one "label v · sha" for the footer's backend / UI
// pair, inline.
function BuildBadge({ label, build }: { label: string; build: BuildInfo }) {
  if (!build.version) return null;
  return (
    <span className="whitespace-nowrap">
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
  );
}

/**
 * The backend and UI builds. One build when they match, which is the usual
 * case and needs saying once; both, in the warning tone, when they differ,
 * so a stale tab against a redeployed backend stands out.
 */
function BuildPair({ backend, ui }: { backend: BuildInfo; ui: BuildInfo }) {
  const same = backend.version === ui.version && backend.commit === ui.commit;
  return (
    <div
      className={cn(
        T.hint,
        "flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1",
        !same && backend.version && ui.version && "text-warning",
      )}
    >
      <AdjustmentsVerticalIcon className="size-4 shrink-0" />
      {same ? (
        <BuildBadge label="build" build={ui} />
      ) : (
        <>
          <BuildBadge label="backend" build={backend} />
          <BuildBadge label="ui" build={ui} />
        </>
      )}
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
  // Until the user is known, render as an operator would see it: the widgets
  // hold skeletons, which is right for both.
  const adminPlane = !user || canUseAdminPlane(user.roles);
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
          Icon: CheckCircleIcon,
        }
      : rollupKey === "DEGRADED"
        ? {
            label: "Degraded",
            color: "text-warning",
            Icon: ExclamationTriangleIcon,
          }
        : rollupKey === "UNHEALTHY" ||
            rollupKey === "ERROR" ||
            rollupKey === "DOWN"
          ? {
              label: "Unhealthy",
              color: "text-destructive",
              Icon: XCircleIcon,
            }
          : {
              label: "Unknown",
              color: "text-muted-foreground",
              Icon: ShieldCheckIcon,
            };
  const RollupIcon = rollupMeta.Icon;

  return (
    <div className="space-y-8">
      <PageHeader
        title="Dashboard"
        description="System health, recent activity, and anything that needs attention."
        actions={
          adminPlane && (
            <Button size="sm" asChild>
              <Link href="/upload">
                <ArrowUpTrayIcon className="size-4" />
                Upload
              </Link>
            </Button>
          )
        }
      />

      {adminPlane ? (
        <>
          <FirstRunChecklist />

          {/* ─── Attention widgets: recent activity, failed ops, budgets ── */}
          <DashboardWidgets />

          {/* ─── Mine and the limits: my operations, quotas near their caps */}
          <div className="grid gap-4 md:grid-cols-2">
            <MyOperationsWidget />
            <QuotaAlertsWidget />
          </div>

          {/* ─── Event-dispatcher backlog (SystemService.GetDispatcherStats) */}
          <DispatcherStatsCard />
        </>
      ) : (
        <NoAdminRole />
      )}

      {/* ─── Footer strip: health rollup · identity · build pair ────── */}
      <Card className="py-0">
        <CardContent className="flex flex-wrap items-center gap-x-5 gap-y-2 py-2.5">
          <div className={cn(T.pill, "shrink-0", rollupMeta.color)}>
            <RollupIcon className="size-4" />
            <span>{rollupMeta.label}</span>
            {components.length > 0 && (
              <span className="font-normal text-muted-foreground">
                · {formatCount(components.length)}{" "}
                {components.length === 1 ? "component" : "components"}
              </span>
            )}
          </div>
          {/* How the components split by state: one failing among many
              shows here before the rollup turns. It takes the room the
              line has left. */}
          <StatusBar
            segments={componentSegments(components)}
            className="min-w-48 flex-1"
          />
          <div className={cn(T.hint, "flex min-w-0 items-center gap-2")}>
            <UserCircleIcon className="size-4 shrink-0" />
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
          <BuildPair backend={backendBuild(stats)} ui={ui} />
        </CardContent>
      </Card>
    </div>
  );
}
