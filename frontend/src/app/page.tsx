"use client";

import { useEffect, useMemo } from "react";
import Link from "next/link";
import {
  AdjustmentsVerticalIcon,
  ArchiveBoxIcon,
  ArrowRightIcon,
  ArrowUpTrayIcon,
  CheckCircleIcon,
  ClipboardDocumentListIcon,
  Cog6ToothIcon,
  CubeIcon,
  ExclamationTriangleIcon,
  KeyIcon,
  ShieldCheckIcon,
  TagIcon,
  TrashIcon,
  UserCircleIcon,
  UsersIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { useTenants } from "@/hooks/useTenants";
import { useBackends } from "@/hooks/useBackends";
import { useBuckets } from "@/hooks/useBuckets";
import { useObjectKeys } from "@/hooks/useObjectKeys";
import { useStats } from "@/context/StatsContext";
import { useAuth } from "@/context/AuthContext";
import {
  type ComponentHealth,
  componentStatusLabel,
} from "@/lib/connect/system";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

// /  — Dashboard / nav grid.
//
// One tile per page in the app, grouped by the same Core / Management /
// System / Settings categories the sidebar uses. Each tile carries a
// short description plus a live count where it makes sense (Tenants
// from ListTenants, Components from SystemService.GetHealth, etc.).
// Pages without a meaningful count (Profile, Policies, Upload …) just
// render the icon + description.
//
// Top of the page keeps a slim status strip (rollup + signed-in user +
// build) so the operator sees system state at a glance.

interface PageTile {
  name: string;
  href: string;
  description: string;
  icon: React.ElementType;
  /** Tailwind class for the icon foreground. */
  accent: string;
  /** Optional live count. null = loading; undefined = no count. */
  count?: number | null;
}

interface PageGroup {
  title: string;
  /** Tailwind class for the group's section heading. */
  accent: string;
  tiles: PageTile[];
}

function PageTileCard({ tile }: { tile: PageTile }) {
  const Icon = tile.icon;
  const showCount = tile.count !== undefined;
  return (
    <Link
      href={tile.href}
      className="group rounded-xl focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <Card className="h-full transition-colors hover:border-primary/40 hover:bg-card/60">
        <CardHeader className="flex flex-row items-start gap-3 px-4">
          <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-secondary">
            <Icon className={cn("size-5", tile.accent)} />
          </div>
          <div className="min-w-0 flex-1 space-y-0.5">
            <CardTitle className="text-sm">{tile.name}</CardTitle>
            <CardDescription className={cn(T.hint, "leading-snug")}>
              {tile.description}
            </CardDescription>
          </div>
          <ArrowRightIcon className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-foreground" />
        </CardHeader>
        {showCount && (
          <CardContent className="px-4 pb-3 pt-0">
            {tile.count === null ? (
              <Skeleton className="h-6 w-12" />
            ) : (
              <span className="font-mono text-lg font-semibold tabular-nums tracking-tight">
                {(tile.count ?? 0).toLocaleString()}
              </span>
            )}
          </CardContent>
        )}
      </Card>
    </Link>
  );
}

export default function DashboardPage() {
  const { user } = useAuth();
  const { tenants, fetchTenants, loading: tenantsLoading } = useTenants();
  const { backends, loading: backendsLoading } = useBackends();
  const { buckets, fetchBuckets, loading: bucketsLoading } = useBuckets();
  const {
    objectKeys,
    fetchObjectKeys,
    loading: objectKeysLoading,
  } = useObjectKeys();
  const { stats, loading: statsLoading } = useStats();

  useEffect(() => {
    void fetchTenants();
    void fetchBuckets();
    void fetchObjectKeys();
  }, [fetchTenants, fetchBuckets, fetchObjectKeys]);

  // ── Status strip data ──────────────────────────────────────────────
  const components: ComponentHealth[] = stats?.health?.components ?? [];
  const rollupKey =
    stats?.health?.status !== undefined
      ? componentStatusLabel(stats.health.status).toUpperCase()
      : "UNKNOWN";
  const rollupMeta =
    rollupKey === "OK" || rollupKey === "HEALTHY"
      ? {
          label: "Healthy",
          color: "text-chart-2",
          dot: "bg-chart-2",
          Icon: CheckCircleIcon,
        }
      : rollupKey === "DEGRADED"
        ? {
            label: "Degraded",
            color: "text-chart-3",
            dot: "bg-chart-3",
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

  // ── Page tiles ─────────────────────────────────────────────────────
  const groups: PageGroup[] = useMemo(
    () => [
      {
        title: "Core",
        accent: "text-primary/80",
        tiles: [
          {
            name: "Objects",
            href: "/objects",
            description: "Browse stored assets across keys.",
            icon: CubeIcon,
            accent: "text-primary/80",
          },
          {
            name: "Upload",
            href: "/upload",
            description: "Stream new files into a tenant ObjectKey.",
            icon: ArrowUpTrayIcon,
            accent: "text-primary/80",
          },
        ],
      },
      {
        title: "Management",
        accent: "text-chart-2/85",
        tiles: [
          {
            name: "Tenants",
            href: "/tenants",
            description: "Multi-tenant workspaces and inherited policy.",
            icon: UsersIcon,
            accent: "text-chart-2/85",
            count: tenantsLoading ? null : tenants.length,
          },
          {
            name: "S3 Buckets",
            href: "/buckets",
            description: "Physical storage backends bound by ObjectKeys.",
            icon: ArchiveBoxIcon,
            accent: "text-chart-2/85",
            count: bucketsLoading ? null : buckets.length,
          },
          {
            name: "Object Keys",
            href: "/object-keys",
            description: "Tenant-scoped namespaces routed to a bucket.",
            icon: ArchiveBoxIcon,
            accent: "text-chart-2/85",
            count: objectKeysLoading ? null : objectKeys.length,
          },
          {
            name: "Object Tags",
            href: "/object-tags",
            description: "Tenant-wide tag inventory and per-object editor.",
            icon: TagIcon,
            accent: "text-chart-2/85",
          },
          {
            name: "Trash",
            href: "/trash",
            description: "Soft-deleted objects awaiting purge.",
            icon: TrashIcon,
            accent: "text-chart-2/85",
          },
          {
            name: "Policies",
            href: "/policies",
            description: "Cedar editor + simulator for the policy graph.",
            icon: ShieldCheckIcon,
            accent: "text-chart-2/85",
          },
        ],
      },
      {
        title: "System",
        accent: "text-chart-3/85",
        tiles: [
          {
            name: "Audit Logs",
            href: "/audit",
            description: "Append-only log of every mutation.",
            icon: ClipboardDocumentListIcon,
            accent: "text-chart-3/85",
          },
          {
            name: "Health Status",
            href: "/health",
            description: "Per-component rollup pulled from SystemService.",
            icon: CheckCircleIcon,
            accent: "text-chart-3/85",
            count: statsLoading ? null : components.length,
          },
        ],
      },
      {
        title: "Settings",
        accent: "text-chart-5/85",
        tiles: [
          {
            name: "Profile",
            href: "/profile",
            description: "Theme, timezone, locale — synced via UserSettings.",
            icon: UserCircleIcon,
            accent: "text-chart-5/85",
          },
          {
            name: "API Tokens",
            href: "/api-tokens",
            description: "Personal access tokens for programmatic use.",
            icon: KeyIcon,
            accent: "text-chart-5/85",
          },
          {
            name: "Configuration",
            href: "/config",
            description: "Cluster snapshot + redacted runtime YAML.",
            icon: Cog6ToothIcon,
            accent: "text-chart-5/85",
            count: backendsLoading ? null : backends.length,
          },
        ],
      },
    ],
    [
      tenants.length,
      tenantsLoading,
      buckets.length,
      bucketsLoading,
      objectKeys.length,
      objectKeysLoading,
      components.length,
      statsLoading,
      backends.length,
      backendsLoading,
    ],
  );

  return (
    <div className="space-y-8">
      <PageHeader
        title="Dashboard"
        description="Jump to any surface in the control plane."
        showDefaultActions={false}
        actions={
          <Button size="sm" asChild>
            <Link href="/upload">
              <ArrowUpTrayIcon className="size-4" />
              Upload
            </Link>
          </Button>
        }
      />

      {/* ─── Status strip ─────────────────────────────────────────── */}
      <Card>
        <CardContent className="flex flex-wrap items-center gap-x-6 gap-y-3 px-5 py-4">
          <div className={cn(T.pill, rollupMeta.color)}>
            <span
              className={cn(
                T.pillDot,
                rollupMeta.dot,
                rollupMeta.label !== "Healthy" && "animate-pulse",
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
          {stats?.version?.version && (
            <div className={cn(T.hint, "ml-auto flex items-center gap-2")}>
              <AdjustmentsVerticalIcon className="size-4" />
              <span>
                build{" "}
                <span className={cn(T.code, "text-foreground")}>
                  {stats.version.version}
                </span>
                {stats.version.commit && (
                  <>
                    {" · "}
                    <span className={cn(T.code, "text-foreground")}>
                      {stats.version.commit.slice(0, 7)}
                    </span>
                  </>
                )}
              </span>
            </div>
          )}
        </CardContent>
      </Card>

      {/* ─── Grouped page tiles ───────────────────────────────────── */}
      {groups.map((group) => (
        <section key={group.title} className="space-y-3">
          <h2 className={cn(T.label, "font-semibold", group.accent)}>
            {group.title}
          </h2>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {group.tiles.map((tile) => (
              <PageTileCard key={tile.href} tile={tile} />
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}
