"use client";

import {
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  InboxIcon,
  ServerIcon,
  ShieldCheckIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useStats } from "@/context/StatsContext";
import { cn } from "@/lib/utils";
import {
  type ComponentHealth,
  componentStatusLabel,
} from "@/lib/connect/system";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/Skeleton";

type HealthState = "healthy" | "degraded" | "unhealthy" | "unknown";

const stateFor = (status: string): HealthState => {
  const s = (status || "").toUpperCase();
  if (s === "OK" || s === "HEALTHY") return "healthy";
  if (s === "DEGRADED") return "degraded";
  if (s === "UNHEALTHY" || s === "ERROR" || s === "DOWN") return "unhealthy";
  return "unknown";
};

const stateMeta: Record<
  HealthState,
  { label: string; icon: React.ElementType; color: string; dot: string }
> = {
  healthy: {
    label: "Healthy",
    icon: CheckCircleIcon,
    color: "text-chart-2",
    dot: "bg-chart-2",
  },
  degraded: {
    label: "Degraded",
    icon: ExclamationTriangleIcon,
    color: "text-chart-3",
    dot: "bg-chart-3",
  },
  unhealthy: {
    label: "Unhealthy",
    icon: XCircleIcon,
    color: "text-destructive",
    dot: "bg-destructive",
  },
  unknown: {
    label: "Unknown",
    icon: ShieldCheckIcon,
    color: "text-muted-foreground",
    dot: "bg-muted-foreground",
  },
};

function DependencyCard({ dep }: { dep: ComponentHealth }) {
  const meta = stateMeta[stateFor(componentStatusLabel(dep.status))];
  const Icon = meta.icon;
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-3 px-4">
        <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-secondary">
          <Icon className={cn("size-5", meta.color)} />
        </div>
        <Badge variant="outline" className="font-mono text-[10px] tabular-nums">
          {Number(dep.latencyMs)}ms
        </Badge>
      </CardHeader>
      <CardContent className="px-4">
        <CardTitle className="text-sm">{dep.name}</CardTitle>
        <div className="mt-1.5 flex items-center gap-1.5">
          <span
            className={cn(
              "size-1.5 rounded-full",
              meta.dot,
              meta.label !== "Healthy" && "animate-pulse",
            )}
          />
          <span className={cn("text-xs font-medium", meta.color)}>
            {meta.label}
          </span>
        </div>
        {dep.message && (
          <p
            className="mt-2 truncate text-xs text-muted-foreground"
            title={dep.message}
          >
            {dep.message}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

// Tight definition-list cell — used by the Overview card so version /
// commit / Go-runtime / latency / sync-time all share the same clean
// label-on-top, value-on-bottom shape instead of the heavier StatCard
// boxes the previous design used (which duplicated the Service-
// components data and made the page feel sparse + overfull at once).
function OverviewItem({
  label,
  value,
  className,
  hint,
}: {
  label: string;
  value: React.ReactNode;
  className?: string;
  hint?: string;
}) {
  return (
    <div className={cn("space-y-0.5", className)}>
      <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      <div className="font-mono text-sm tabular-nums">{value}</div>
      {hint && <div className="text-[11px] text-muted-foreground">{hint}</div>}
    </div>
  );
}

export default function HealthPage() {
  const { stats, loading, lastUpdated, refresh } = useStats();
  const components: ComponentHealth[] = stats?.health?.components ?? [];

  const rollupKey =
    stats?.health?.status !== undefined
      ? stateFor(componentStatusLabel(stats.health.status))
      : "unknown";
  const rollup = stateMeta[rollupKey];
  const RollupIcon = rollup.icon;

  const healthyCount = components.filter(
    (c) => stateFor(componentStatusLabel(c.status)) === "healthy",
  ).length;
  const maxLatency = components.reduce(
    (max, c) => (Number(c.latencyMs) > max ? Number(c.latencyMs) : max),
    0,
  );

  const version = stats?.version?.version || "—";
  const commit = stats?.version?.commit
    ? stats.version.commit.slice(0, 10)
    : "—";
  const goVersion = stats?.version?.goVersion || "—";
  const lastSync = lastUpdated ? lastUpdated.toLocaleTimeString() : "—";

  return (
    <div className="space-y-6">
      <PageHeader
        title="Health & Diagnostics"
        description="Component status and rollup view of the control plane."
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="icon"
            onClick={() => refresh()}
            aria-label="Refresh health data"
          >
            <ArrowPathIcon
              className={cn("size-4", loading && "animate-spin")}
            />
          </Button>
        }
      />

      {/* ─── Single overview card (replaces the previous 4-up
            AnalyticsDashboard + duplicate side card). Header carries
            the rollup; the content row holds everything else as
            tight definition-list cells. ─────────────────────────── */}
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3 px-5">
          <div className="flex items-center gap-3">
            <div
              className={cn(
                "flex size-9 shrink-0 items-center justify-center rounded-md bg-secondary",
                rollup.color,
              )}
            >
              <RollupIcon className="size-5" />
            </div>
            <div>
              <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
                Rollup status
              </div>
              <div
                className={cn(
                  "flex items-center gap-2 text-lg font-semibold tracking-tight",
                  rollup.color,
                )}
              >
                {rollup.label}
                <span
                  className={cn(
                    "size-1.5 rounded-full",
                    rollup.dot,
                    rollup.label !== "Healthy" && "animate-pulse",
                  )}
                />
              </div>
            </div>
          </div>
          <div className="text-right text-[11px] text-muted-foreground">
            <div className="font-medium uppercase tracking-wider">
              Last sync
            </div>
            <div className="font-mono">{lastSync}</div>
          </div>
        </CardHeader>
        <Separator />
        <CardContent className="grid grid-cols-2 gap-4 px-5 py-4 sm:grid-cols-3 lg:grid-cols-6">
          {loading && !stats ? (
            [...Array(6)].map((_, i) => (
              <Skeleton key={i} className="h-12 rounded" />
            ))
          ) : (
            <>
              <OverviewItem
                label="Components"
                value={
                  components.length === 0
                    ? "—"
                    : `${healthyCount}/${components.length}`
                }
                hint="healthy"
              />
              <OverviewItem
                label="Max latency"
                value={components.length === 0 ? "—" : `${maxLatency} ms`}
                hint="across components"
              />
              <OverviewItem label="Version" value={version} />
              <OverviewItem label="Commit" value={commit} />
              <OverviewItem label="Go runtime" value={goVersion} />
              <OverviewItem
                label="Build"
                value={
                  stats?.version?.buildTime
                    ? new Date(
                        Number(stats.version.buildTime.seconds) * 1000,
                      ).toLocaleDateString(undefined, {
                        month: "short",
                        day: "numeric",
                      })
                    : "dev"
                }
              />
            </>
          )}
        </CardContent>
      </Card>

      {/* ─── Service components ─────────────────────────────────── */}
      <Card>
        <CardHeader className="flex flex-row items-center gap-2 px-6">
          <ServerIcon className="size-4 text-muted-foreground" />
          <CardTitle className="text-sm">Service components</CardTitle>
        </CardHeader>
        <Separator />
        <CardContent className="px-6 pb-4 pt-2">
          {loading && components.length === 0 ? (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
              {[0, 1, 2, 3].map((i) => (
                <Skeleton key={i} className="h-[124px] rounded-xl" />
              ))}
            </div>
          ) : components.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-2 py-12 text-muted-foreground">
              <InboxIcon className="size-8 opacity-50" />
              <p className="text-sm">No components reported.</p>
              <p className="text-xs">
                Infrastructure components will appear here when the backend
                reports them.
              </p>
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
              {components.map((dep, i) => (
                <DependencyCard key={dep.name || i} dep={dep} />
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
