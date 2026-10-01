"use client";

// Platform statistics — the operator's census of what this control plane
// actually holds: how many tenants / storage backends / buckets / object
// keys / users exist, how they break down, and how many objects sit in
// each lifecycle state per tenant.
//
// One RPC backs the whole page (admin/v1.SystemService.GetPlatformStats).
// It answers from two places: the inventory counts come from the admin
// pod's own pool, while everything under `rls` (objects, quotas,
// capabilities, M2M tokens, event subscriptions) is proxied from the
// worker pod — the only role with a cross-tenant BYPASSRLS pool. That
// second leg can be absent while the first is fine, so every card fed by
// it says "unavailable" rather than rendering a misleading all-zero view.

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowPathIcon,
  ArchiveBoxIcon,
  BoltIcon,
  ChartPieIcon,
  CircleStackIcon,
  CpuChipIcon,
  CubeIcon,
  ServerStackIcon,
  ShieldCheckIcon,
  UserGroupIcon,
  UsersIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { adminSystemClient } from "@/lib/connect/client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { T } from "@/lib/ui/typography";
import { cn, formatBytes, timestampToDate } from "@/lib/utils";
import type {
  ObjectStateStat,
  QuotaStats,
  TenantObjectStats,
} from "@/gen/paladin/admin/v1/system_service_pb";
import { formatCount, formatTime } from "@/lib/format/locale";
import { errorMessage } from "@/hooks/errorContract";

// Poll cadence. Slower than /health's 15s: this is inventory, which moves
// on operator actions and bulk uploads rather than second-to-second, and
// the object census is a GROUP BY over every row in `objects`.
const POLL_INTERVAL = 30_000;

// Column order for the per-tenant object table. Pinned to the lifecycle
// order the backend sorts by, so the header row is stable even when a
// tenant has no rows in a given state. A state the backend reports that
// isn't listed here still renders — see `stateColumns`.
const KNOWN_STATES = ["PENDING", "AVAILABLE", "FAILED", "DELETED"] as const;

// Per-state accent. AVAILABLE is the healthy steady state, FAILED always
// warrants attention, PENDING is only interesting if it stays high, and
// DELETED is history. Muted everywhere the number is zero (see StateCell).
const STATE_ACCENT: Record<string, string> = {
  PENDING: "text-chart-3",
  AVAILABLE: "text-chart-2",
  FAILED: "text-destructive",
  DELETED: "text-muted-foreground",
};

function num(n: bigint | number | undefined): string {
  if (n === undefined) return "—";
  return formatCount(Number(n));
}

// ─── Building blocks ────────────────────────────────────────────────────────

/** Headline metric: big number over a label, optional secondary line. */
function Metric({
  label,
  value,
  sub,
  icon: Icon,
  accent,
}: {
  label: string;
  value: string;
  sub?: string;
  icon?: React.ElementType;
  accent?: string;
}) {
  return (
    <div className="flex items-start gap-2.5">
      {Icon && (
        <Icon className="mt-0.5 size-5 shrink-0 text-muted-foreground" />
      )}
      <div className="min-w-0">
        <div className={T.label}>{label}</div>
        <div className={cn("text-2xl font-mono tabular-nums", accent)}>
          {value}
        </div>
        {sub && <div className={T.hint}>{sub}</div>}
      </div>
    </div>
  );
}

/** label → value row inside a breakdown card. */
function Row({
  label,
  value,
  accent,
  mono = true,
}: {
  label: string;
  value: string;
  accent?: string;
  mono?: boolean;
}) {
  return (
    <div className="flex items-baseline justify-between gap-3 py-1">
      <span className={cn(T.body, "truncate text-muted-foreground")}>
        {label}
      </span>
      <span
        className={cn(
          mono ? "font-mono tabular-nums" : "",
          "text-sm shrink-0",
          accent,
        )}
      >
        {value}
      </span>
    </div>
  );
}

/**
 * Renders a proto map<string,int64> as sorted rows. Sorted by count
 * descending then key, so the busiest backend / most common kind reads
 * first and the order doesn't jitter between polls.
 */
function MapRows({
  map,
  empty,
}: {
  map: Record<string, bigint>;
  empty: string;
}) {
  const entries = Object.entries(map ?? {}).sort(
    (a, b) => Number(b[1] - a[1]) || a[0].localeCompare(b[0]),
  );
  if (entries.length === 0) {
    return <p className={T.hint}>{empty}</p>;
  }
  return (
    <>
      {entries.map(([k, v]) => (
        <Row key={k} label={k} value={num(v)} />
      ))}
    </>
  );
}

function BreakdownCard({
  title,
  icon: Icon,
  badge,
  children,
}: {
  title: string;
  icon: React.ElementType;
  badge?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Card className="flex flex-col">
      <CardHeader className="flex flex-row items-center gap-2 px-4 py-3">
        <Icon className="size-5 shrink-0 text-muted-foreground" />
        <CardTitle className={cn(T.cardTitleProse, "min-w-0 truncate")}>
          {title}
        </CardTitle>
        {badge && <div className="ml-auto shrink-0">{badge}</div>}
      </CardHeader>
      <Separator />
      <CardContent className="flex-1 px-4 py-3">{children}</CardContent>
    </Card>
  );
}

/**
 * Shared "live / unavailable" pill. Every card fed by the worker-proxied
 * leg carries one — they all fail together (one source, one flag), and an
 * operator should be able to tell at a glance which half of the page is
 * still trustworthy.
 */
function RLSBadge({ up }: { up: boolean }) {
  return (
    <Badge variant="outline" className={up ? "text-chart-2" : "text-chart-3"}>
      {up ? "live" : "unavailable"}
    </Badge>
  );
}

/** Explains the degraded state once, in the operator's terms. */
function CensusUnavailable({ className }: { className?: string }) {
  return (
    <p className={cn("text-sm text-muted-foreground", className)}>
      This census comes from the worker pod, the only role holding a
      cross-tenant (BYPASSRLS) database pool. It is unreachable or unconfigured
      in this deployment — set{" "}
      <code className={T.codeSmall}>worker.ops_url</code> to the worker&rsquo;s
      in-cluster ops URL to enable it.
    </p>
  );
}

/**
 * Breakdown card whose body depends on the worker leg: renders the badge in
 * the header and swaps the body for the explanation when the leg is down,
 * so each card degrades on its own terms rather than vanishing.
 */
function RLSCard({
  title,
  icon,
  up,
  children,
}: {
  title: string;
  icon: React.ElementType;
  up: boolean;
  children: React.ReactNode;
}) {
  return (
    <BreakdownCard title={title} icon={icon} badge={<RLSBadge up={up} />}>
      {up ? children : <CensusUnavailable />}
    </BreakdownCard>
  );
}

/**
 * The quota "usage" counters next to a plain-language note on what they
 * actually mean.
 *
 * This is the one number on the page that is NOT a live measurement. The
 * `quotas.usage_*` columns are incremented best-effort on promote and
 * recomputed from live objects by the worker's quota reconciler, so they
 * converge on the census but can trail it by up to one reconcile interval.
 * The census counts rows at read time and is therefore authoritative; the
 * counter is shown because it is what quota ENFORCEMENT compares against,
 * so an operator debugging an unexpected ResourceExhausted needs to see
 * the number the rejection was actually made from.
 *
 * The coverage line matters as much as the numbers: quotas are opt-in, so
 * a fleet where only some tenants have a quota row has a counter that was
 * never meant to total the whole estate. Without that line the two numbers
 * invite a subtraction that means nothing.
 */
function QuotaDrift({
  quotas,
  activeTenants,
}: {
  quotas?: QuotaStats;
  activeTenants?: bigint;
}) {
  const covered = quotas?.tenantScoped ?? 0n;
  return (
    <>
      <div className={cn(T.label, "pb-1")}>Enforced usage</div>
      <Row label="Objects counted" value={num(quotas?.usageObjectCount)} />
      <Row
        label="Bytes counted"
        value={formatBytes(quotas?.usageTotalBytes ?? 0n)}
      />
      <p className={cn(T.hint, "pt-1.5 leading-relaxed")}>
        Covers {num(covered)} of {num(activeTenants)} active tenants — quotas
        are opt-in. This is the number upload rejections are made from,
        reconciled from live objects on an interval, so it can trail the object
        census below by one reconcile period. The census is the authoritative
        count of what is stored.
      </p>
    </>
  );
}

// ─── Object census ──────────────────────────────────────────────────────────

/** Index a repeated ObjectStateStat by state name for O(1) column lookup. */
function byState(states: ObjectStateStat[]): Record<string, ObjectStateStat> {
  const out: Record<string, ObjectStateStat> = {};
  for (const s of states) out[s.state] = s;
  return out;
}

function StateCell({ stat }: { stat?: ObjectStateStat }) {
  const count = stat?.count ?? 0n;
  return (
    <TableCell
      className={cn(
        "text-right font-mono tabular-nums",
        count === 0n
          ? "text-muted-foreground/50"
          : STATE_ACCENT[stat?.state ?? ""],
      )}
    >
      {num(count)}
    </TableCell>
  );
}

function TenantRow({
  tenant,
  columns,
}: {
  tenant: TenantObjectStats;
  columns: string[];
}) {
  const states = byState(tenant.states);
  // slug is the handle operators actually navigate by; fall back to the
  // display name and then the raw UUID (a tenant row purged while its
  // objects lingered has neither).
  const label = tenant.slug || tenant.displayName || tenant.tenantId;
  return (
    <TableRow>
      <TableCell className="max-w-72">
        <div className="truncate font-mono text-sm">{label}</div>
        {tenant.slug && tenant.displayName && (
          <div className={cn(T.hint, "truncate")}>{tenant.displayName}</div>
        )}
      </TableCell>
      {columns.map((s) => (
        <StateCell key={s} stat={states[s]} />
      ))}
      <TableCell className="text-right font-mono tabular-nums font-medium">
        {num(tenant.totalCount)}
      </TableCell>
      <TableCell className="text-right font-mono tabular-nums text-muted-foreground">
        {formatBytes(tenant.totalBytes)}
      </TableCell>
    </TableRow>
  );
}

// ─── Page ───────────────────────────────────────────────────────────────────

export default function StatsPage() {
  const { data, isLoading, isFetching, error, refetch } = useQuery({
    queryKey: ["platformStats"],
    queryFn: () => adminSystemClient.getPlatformStats({}),
    refetchInterval: POLL_INTERVAL,
    retry: false,
  });

  const rls = data?.rls;
  const rlsUp = !!rls?.available;
  const objects = rls?.objects;

  // Column set = the pinned lifecycle order, plus any state the backend
  // reported that we don't know about yet (a new object_state enum member
  // shows up here without a frontend change).
  const stateColumns = useMemo(() => {
    const seen = new Set<string>();
    for (const s of objects?.states ?? []) seen.add(s.state);
    for (const t of objects?.tenants ?? []) {
      for (const s of t.states) seen.add(s.state);
    }
    const known = KNOWN_STATES.filter((s) => seen.has(s));
    const extra = [...seen].filter((s) => !KNOWN_STATES.includes(s as never));
    return [...known, ...extra.sort()];
  }, [objects]);

  const globalStates = useMemo(() => byState(objects?.states ?? []), [objects]);

  const collectedDate = data?.collectedAt
    ? timestampToDate(data.collectedAt)
    : null;
  const collectedAt = collectedDate ? formatTime(collectedDate) : null;

  return (
    <div className="space-y-4">
      <PageHeader
        title="Platform Statistics"
        description="Cross-tenant census of tenants, storage, and objects."
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="icon"
            onClick={() => void refetch()}
            aria-label="Refresh"
          >
            <ArrowPathIcon
              className={cn("size-4", isFetching && "animate-spin")}
            />
          </Button>
        }
      />

      {error && (
        <Card>
          <CardContent className="py-4 text-sm text-destructive">
            {errorMessage(error, "Failed to load platform statistics")}
          </CardContent>
        </Card>
      )}

      {isLoading && !data ? (
        <>
          <Skeleton className="h-23 rounded-xl" />
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-50 rounded-xl" />
            ))}
          </div>
        </>
      ) : data ? (
        <>
          {/* ─── Headline strip ───────────────────────────────────────── */}
          <Card>
            <CardContent className="flex flex-wrap items-start gap-x-8 gap-y-4 px-5 py-4">
              <Metric
                label="Tenants"
                value={num(data.tenants?.active)}
                sub={`${num(data.tenants?.trashed)} in trash`}
                icon={UsersIcon}
              />
              <Metric
                label="Backends"
                value={num(data.backends?.total)}
                sub={`${num(data.backends?.enabled)} enabled`}
                icon={ServerStackIcon}
              />
              <Metric
                label="Buckets"
                value={num(data.buckets?.total)}
                sub={`${num(data.buckets?.tenantOwned)} tenant-owned`}
                icon={ArchiveBoxIcon}
              />
              <Metric
                label="Collections"
                value={num(data.collections?.total)}
                sub={`${num(data.collections?.unbound)} unbound`}
                icon={CircleStackIcon}
              />
              <Metric
                label="Users"
                value={num(data.users?.total)}
                sub={`${num(data.users?.disabled)} disabled`}
                icon={UserGroupIcon}
              />
              <Metric
                label="Objects"
                value={rlsUp ? num(objects?.totalCount) : "—"}
                sub={
                  rlsUp
                    ? formatBytes(objects?.totalBytes ?? 0n)
                    : "census unavailable"
                }
                icon={CubeIcon}
              />
              <div className="ml-auto text-right">
                <div className={T.label}>As of</div>
                <div className={T.value}>{collectedAt ?? "—"}</div>
              </div>
            </CardContent>
          </Card>

          {/* ─── Inventory breakdowns ─────────────────────────────────── */}
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
            <BreakdownCard title="Tenants" icon={UsersIcon}>
              <Row label="Active" value={num(data.tenants?.active)} />
              <Row
                label="Trashed"
                value={num(data.tenants?.trashed)}
                accent={
                  (data.tenants?.trashed ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
              <Separator className="my-2" />
              <Row
                label="Shared layout"
                value={num(data.tenants?.sharedLayout)}
              />
              <Row
                label="Dedicated layout"
                value={num(data.tenants?.dedicatedLayout)}
              />
              <Separator className="my-2" />
              <Row
                label="No default binding"
                value={num(data.tenants?.withoutDefaultBinding)}
                accent={
                  (data.tenants?.withoutDefaultBinding ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
            </BreakdownCard>

            <BreakdownCard title="Storage backends" icon={ServerStackIcon}>
              <Row label="Enabled" value={num(data.backends?.enabled)} />
              <Row
                label="Disabled"
                value={num(data.backends?.disabled)}
                accent={
                  (data.backends?.disabled ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
              <Row
                label="Read-only (draining)"
                value={num(data.backends?.readOnly)}
                accent={
                  (data.backends?.readOnly ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
              <Row
                label="Maintenance"
                value={num(data.backends?.maintenance)}
                accent={
                  (data.backends?.maintenance ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
              <Separator className="my-2" />
              <div className={cn(T.label, "pb-1")}>By kind</div>
              <MapRows
                map={data.backends?.byKind ?? {}}
                empty="No backends registered."
              />
            </BreakdownCard>

            <BreakdownCard title="Buckets" icon={ArchiveBoxIcon}>
              <Row
                label="Tenant-owned"
                value={num(data.buckets?.tenantOwned)}
              />
              <Row label="Shared" value={num(data.buckets?.shared)} />
              <Separator className="my-2" />
              <Row
                label="Versioning on"
                value={num(data.buckets?.versioningEnabled)}
              />
              <Row
                label="Object lock on"
                value={num(data.buckets?.objectLockEnabled)}
              />
              <Row
                label="Replication on"
                value={num(data.buckets?.replicationEnabled)}
              />
              <Separator className="my-2" />
              <div className={cn(T.label, "pb-1")}>By provision state</div>
              <MapRows
                map={data.buckets?.byProvisionState ?? {}}
                empty="No buckets."
              />
            </BreakdownCard>

            <BreakdownCard title="Collections" icon={CircleStackIcon}>
              <Row label="Total" value={num(data.collections?.total)} />
              <Row
                label="Unbound (no bucket)"
                value={num(data.collections?.unbound)}
                accent={
                  (data.collections?.unbound ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
              <Separator className="my-2" />
              <div className={cn(T.label, "pb-1")}>By backend</div>
              <MapRows
                map={data.collections?.byBackend ?? {}}
                empty="No collections."
              />
            </BreakdownCard>
          </div>

          {/* ─── RLS'd inventory: quotas, capabilities, tokens, events ─── */}
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
            <RLSCard title="Quotas" icon={ChartPieIcon} up={rlsUp}>
              <Row
                label="Tenant-scoped"
                value={num(rls?.quotas?.tenantScoped)}
              />
              <Row
                label="Bucket-scoped"
                value={num(rls?.quotas?.bucketScoped)}
              />
              <Row
                label="Enforcing a cap"
                value={num(rls?.quotas?.withLimits)}
              />
              <Separator className="my-2" />
              <Row
                label="At / over limit"
                value={num(rls?.quotas?.atLimit)}
                accent={
                  (rls?.quotas?.atLimit ?? 0n) > 0n
                    ? "text-destructive"
                    : undefined
                }
              />
              <Row
                label="Near limit (≥90%)"
                value={num(rls?.quotas?.nearLimit)}
                accent={
                  (rls?.quotas?.nearLimit ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
              <Separator className="my-2" />
              <QuotaDrift
                quotas={rls?.quotas}
                activeTenants={data.tenants?.active}
              />
            </RLSCard>

            <RLSCard title="Capabilities" icon={ShieldCheckIcon} up={rlsUp}>
              <Row label="Active" value={num(rls?.capabilities?.active)} />
              <Row label="Expired" value={num(rls?.capabilities?.expired)} />
              <Row label="Revoked" value={num(rls?.capabilities?.revoked)} />
              <Separator className="my-2" />
              <Row
                label="Delegated"
                value={num(rls?.capabilities?.delegated)}
              />
              <Row
                label="Expiring < 24h"
                value={num(rls?.capabilities?.expiringSoon)}
                accent={
                  (rls?.capabilities?.expiringSoon ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
              <Separator className="my-2" />
              <div className={cn(T.label, "pb-1")}>Active by principal</div>
              <MapRows
                map={rls?.capabilities?.byPrincipalKind ?? {}}
                empty="No active capabilities."
              />
            </RLSCard>

            <RLSCard title="M2M tokens" icon={CpuChipIcon} up={rlsUp}>
              <Row label="Active" value={num(rls?.apiTokens?.active)} />
              <Row label="Expired" value={num(rls?.apiTokens?.expired)} />
              <Row label="Revoked" value={num(rls?.apiTokens?.revoked)} />
              <Separator className="my-2" />
              <Row
                label="Expiring < 7d"
                value={num(rls?.apiTokens?.expiringSoon)}
                accent={
                  (rls?.apiTokens?.expiringSoon ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
              <Row
                label="Never used"
                value={num(rls?.apiTokens?.neverUsed)}
                accent={
                  (rls?.apiTokens?.neverUsed ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
            </RLSCard>

            <RLSCard title="Event subscriptions" icon={BoltIcon} up={rlsUp}>
              <Row label="Enabled" value={num(rls?.subscriptions?.enabled)} />
              <Row
                label="Disabled"
                value={num(rls?.subscriptions?.disabled)}
                accent={
                  (rls?.subscriptions?.disabled ?? 0n) > 0n
                    ? "text-chart-3"
                    : undefined
                }
              />
              <Row
                label="With CEL filter"
                value={num(rls?.subscriptions?.withFilter)}
              />
              <Separator className="my-2" />
              <div className={cn(T.label, "pb-1")}>Enabled by sink</div>
              <MapRows
                map={rls?.subscriptions?.bySinkKind ?? {}}
                empty="No enabled subscriptions."
              />
            </RLSCard>
          </div>

          {/* ─── Object census ────────────────────────────────────────── */}
          <Card>
            <CardHeader className="flex flex-row items-center justify-between gap-2 px-4 py-3">
              <div className="flex items-center gap-2">
                <CubeIcon className="size-5 shrink-0 text-muted-foreground" />
                <CardTitle className={T.cardTitleProse}>
                  Objects by state, per tenant
                </CardTitle>
              </div>
              <RLSBadge up={rlsUp} />
            </CardHeader>
            <Separator />
            <CardContent className="px-0 py-0">
              {!rlsUp || !objects ? (
                <CensusUnavailable className="px-4 py-6" />
              ) : objects.tenants.length === 0 ? (
                <p className="px-4 py-6 text-sm text-muted-foreground">
                  No objects stored yet.
                </p>
              ) : (
                <div className="overflow-x-auto">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Tenant</TableHead>
                        {stateColumns.map((s) => (
                          <TableHead key={s} className="text-right">
                            {s.toLowerCase()}
                          </TableHead>
                        ))}
                        <TableHead className="text-right">Total</TableHead>
                        <TableHead className="text-right">Size</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {objects.tenants.map((t) => (
                        <TenantRow
                          key={t.tenantId}
                          tenant={t}
                          columns={stateColumns}
                        />
                      ))}
                      {/* Fleet rollup. Kept in the body rather than a
                          <tfoot> so it scrolls with the rows on narrow
                          viewports instead of floating over them. */}
                      <TableRow className="border-t-2 font-medium">
                        <TableCell>All tenants</TableCell>
                        {stateColumns.map((s) => (
                          <StateCell key={s} stat={globalStates[s]} />
                        ))}
                        <TableCell className="text-right font-mono tabular-nums">
                          {num(objects.totalCount)}
                        </TableCell>
                        <TableCell className="text-right font-mono tabular-nums text-muted-foreground">
                          {formatBytes(objects.totalBytes)}
                        </TableCell>
                      </TableRow>
                    </TableBody>
                  </Table>
                  {objects.tenantsTruncated > 0n && (
                    <p className={cn(T.hint, "px-4 py-2")}>
                      {num(objects.tenantsTruncated)} smaller tenant(s) omitted
                      — the rollup row still counts them.
                    </p>
                  )}
                </div>
              )}
            </CardContent>
          </Card>
        </>
      ) : null}
    </div>
  );
}
