"use client";

import { IconTone } from "@/components/ui/IconChip";
import { Suspense, useCallback, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  ArrowPathIcon,
  ChartBarIcon,
  TableCellsIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card, CardTitle, CardHeader, CardContent } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Select } from "@/components/ui/Select";
import { SortableHead } from "@/components/ui/SortHeader";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { ActorName } from "@/components/features/audit/ActorName";
import { SpendChart } from "@/components/features/billing/SpendChart";
import {
  fillSeries,
  type Granularity,
} from "@/components/features/billing/spendSeries";
import {
  DEFAULT_PRESET,
  GRANULARITIES,
  PRESETS,
  allowedGranularities,
  isPeriod,
  pickGranularity,
  resolvePeriod,
  type PresetKey,
} from "@/components/features/billing/period";
import { useTableSort } from "@/hooks/useTableSort";
import { usePlatformCurrency } from "@/hooks/usePlatformCurrency";
import { useTenants } from "@/hooks/useTenants";
import { errorMessage } from "@/hooks/errorContract";
import { useScope } from "@/context/ScopeContext";
import { billingClient } from "@/lib/connect/client";
import { normalizeError } from "@/lib/connect/error";
import { sortRows, type SortAccessors } from "@/lib/table/view";
import { tenantBudgetHref, tenantHref } from "@/lib/navigation/hrefs";
import {
  ABSTRACT_UNIT_CODE,
  formatMoney,
  moneyFromNanos,
  moneyPercent,
  moneyToNanos,
  moneyToNumber,
} from "@/lib/format/money";
import {
  budgetLimit,
  budgetUnit,
  budgetUnlimited,
} from "@/lib/format/budgetCeiling";
import { formatDate } from "@/lib/format/locale";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import type { TopEntry } from "@/gen/paladin/admin/v1/billing_service_pb";
import { Count } from "@/components/ui/Count";

// /billing — one tenant's capability spend over a period, from the charges
// ledger (BillingService): what it came to against its budget, how it moved
// over time, and which capabilities, actors and operations it went to.
//
// The tenant is the one ?tenant= names, else the console's scope, so a link
// opens the same view. A summary across every tenant waits on the ledger's
// API (BACKLOG.md).

const TENANT_PARAM = "tenant";

// Spend is shown to four decimals so a sub-cent charge does not read as
// nothing.
const SPEND_DIGITS = 4;
// A budget this far used is close; this far, all but gone.
const BUDGET_WARN_PERCENT = 70;
const BUDGET_CRITICAL_PERCENT = 90;
const FULL_PERCENT = 100;
// The chart takes numbers of whole units.
const NANOS_PER_UNIT = 1e9;

export default function BillingPage() {
  // useSearchParams requires a Suspense boundary under the Next.js app router.
  return (
    <Suspense>
      <BillingView />
    </Suspense>
  );
}

function BillingView() {
  const { tenantId: scopeTenantId } = useScope();
  const router = useRouter();
  const search = useSearchParams();
  const tenantId = search.get(TENANT_PARAM) || scopeTenantId || undefined;
  const platformCurrency = usePlatformCurrency();

  // ── tenants ─────────────────────────────────────────────────────────
  const {
    tenants: allTenants,
    error: tenantsError,
    refetch: refetchTenants,
    fetchedAt: tenantsFetchedAt,
  } = useTenants("all");
  // Trashed tenants too: their charges stay on the ledger.
  // Undefined until the first read, as the picker below distinguishes.
  const tenants = tenantsFetchedAt ? allTenants : undefined;
  const tenantOptions = useMemo(
    () =>
      (tenants ?? [])
        .map((t) => ({
          value: t.tenantId,
          label: `${t.displayName || t.slug}${t.deletedAt ? " · in trash" : ""}`,
        }))
        .sort((a, b) => a.label.localeCompare(b.label)),
    [tenants],
  );
  const tenant = tenants?.find((t) => t.tenantId === tenantId);
  const tenantName =
    tenantOptions.find((o) => o.value === tenantId)?.label ?? "This tenant";
  const tenantHandle = tenant?.slug || tenantId;
  const selectTenant = (id: string) => {
    const next = new URLSearchParams(search.toString());
    next.set(TENANT_PARAM, id);
    router.replace(`?${next.toString()}`);
  };

  // ── period ──────────────────────────────────────────────────────────
  const [preset, setPreset] = useState<PresetKey>(DEFAULT_PRESET);
  const [customStart, setCustomStart] = useState("");
  const [customEnd, setCustomEnd] = useState("");
  const [wanted, setWanted] = useState<Granularity>("day");
  // "Now" for a preset, held until a refresh: a fresh instant per render
  // would make every render a new query.
  const [asOf, setAsOf] = useState(() => new Date());
  const resolved = useMemo(
    () => resolvePeriod(preset, customStart, customEnd, asOf),
    [preset, customStart, customEnd, asOf],
  );
  const period = isPeriod(resolved) ? resolved : null;
  const granularity = period ? pickGranularity(period, wanted) : wanted;
  const allowed = period ? allowedGranularities(period) : [];

  // ── data ────────────────────────────────────────────────────────────
  const range = period && {
    periodStart: timestampFromDate(period.start),
    periodEnd: timestampFromDate(period.end),
  };
  const ready = Boolean(tenantId && range);
  const key = [tenantId, period?.start.getTime(), period?.end.getTime()];
  const summaryQuery = useQuery({
    queryKey: ["billing", "summary", ...key],
    enabled: ready,
    queryFn: async ({ signal }) => {
      try {
        return await billingClient.getTenantSummary(
          { tenantId, ...range },
          { signal },
        );
      } catch (err) {
        throw normalizeError(err);
      }
    },
  });
  const seriesQuery = useQuery({
    queryKey: ["billing", "series", ...key, granularity],
    enabled: ready,
    queryFn: async ({ signal }) => {
      try {
        return await billingClient.getTenantTimeSeries(
          { tenantId, ...range, granularity },
          { signal },
        );
      } catch (err) {
        throw normalizeError(err);
      }
    },
  });
  const summary = summaryQuery.data;
  const refreshing = summaryQuery.isFetching || seriesQuery.isFetching;
  const refresh = useCallback(() => setAsOf(new Date()), []);

  // ── derived ─────────────────────────────────────────────────────────
  const total = summary?.total;
  const unit =
    total?.currencyCode ||
    budgetUnit(summary?.budgetCeiling) ||
    platformCurrency ||
    ABSTRACT_UNIT_CODE;
  // Unset with no budget row; a cap of zero is a cap.
  const max = budgetLimit(summary?.budgetCeiling);
  const capped = max !== undefined;
  const unlimited = budgetUnlimited(summary?.budgetCeiling);
  const chargeCount = summary ? Number(summary.chargeCount) : 0;
  const remainingNanos = moneyToNanos(max) - moneyToNanos(total);
  const remaining = moneyFromNanos(
    remainingNanos > 0n ? remainingNanos : 0n,
    unit,
  );
  // A cap of zero is reached before anything is spent.
  const pctOfBudget = capped
    ? Math.min(FULL_PERCENT, moneyPercent(total, max) ?? FULL_PERCENT)
    : 0;

  const buckets = useMemo(
    () => seriesQuery.data?.buckets ?? [],
    [seriesQuery.data],
  );
  const seriesUnit =
    buckets.find((b) => b.spent?.currencyCode)?.spent?.currencyCode || unit;
  // The axis covers the whole period, so a quiet day reads as a gap.
  const points = useMemo(() => {
    const raw = buckets.flatMap((b) =>
      b.start
        ? [
            {
              start: timestampDate(b.start),
              value: moneyToNumber(b.spent),
              chargeCount: Number(b.chargeCount),
              spent: b.spent,
            },
          ]
        : [],
    );
    return period
      ? fillSeries(raw, period.start, period.end, granularity)
      : raw;
  }, [buckets, period, granularity]);
  const formatAxis = useCallback(
    (v: number) =>
      formatMoney(
        moneyFromNanos(BigInt(Math.round(v * NANOS_PER_UNIT)), seriesUnit),
      ),
    [seriesUnit],
  );
  const money = (m: typeof total, fallbackUnit = unit) =>
    formatMoney(m, { fallbackUnit, fractionDigits: SPEND_DIGITS });
  const peak = buckets.reduce<(typeof buckets)[number] | null>(
    (acc, b) =>
      !acc || moneyToNanos(b.spent) > moneyToNanos(acc.spent) ? b : acc,
    null,
  );
  // Over every bucket of the period, the quiet ones included.
  const avgNanos =
    points.length === 0
      ? 0n
      : buckets.reduce((acc, b) => acc + moneyToNanos(b.spent), 0n) /
        BigInt(points.length);

  const loadError = summaryQuery.error ?? seriesQuery.error;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Billing"
        description="One tenant's capability spend from the charges ledger: against its budget, over time, and what it went to."
        showDefaultActions={false}
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={refresh}
            disabled={refreshing || !ready}
          >
            <ArrowPathIcon
              className={cn("size-4", refreshing && "animate-spin")}
            />
            Refresh
          </Button>
        }
      />

      <div className="flex flex-wrap items-end gap-3">
        <div className="space-y-1">
          <span className={cn(T.label, "block")}>Tenant</span>
          <Select
            aria-label="Tenant"
            value={tenantId}
            onChange={selectTenant}
            options={tenantOptions}
            placeholder="Choose a tenant"
            className="w-64 min-w-0 [&_[data-slot=select-value]]:truncate"
          />
        </div>
        <div className="space-y-1">
          <span className={cn(T.label, "block")}>Period</span>
          <Segmented
            label="Period"
            options={PRESETS.map((p) => ({ value: p.key, label: p.label }))}
            value={preset}
            onChange={setPreset}
          />
        </div>
        {preset === "custom" ? (
          <div className="flex items-center gap-2">
            <Input
              type="date"
              value={customStart}
              onChange={(e) => setCustomStart(e.target.value)}
              className="w-40"
              aria-label="Period start"
            />
            <span className={T.helper}>to</span>
            <Input
              type="date"
              value={customEnd}
              onChange={(e) => setCustomEnd(e.target.value)}
              className="w-40"
              aria-label="Period end"
            />
          </div>
        ) : null}
        <p
          className={cn(
            "pb-1.5 text-sm",
            period ? "text-muted-foreground" : "text-destructive",
          )}
        >
          {period
            ? `${formatDate(period.start)} – ${formatDate(period.end)}`
            : "error" in resolved
              ? resolved.error
              : null}
        </p>
      </div>
      {tenantsError && (
        <ListLoadError
          variant="inline"
          what="Tenants"
          reason={tenantsError}
          onRetry={refetchTenants}
        />
      )}

      {loadError && !summary ? (
        <Card className="space-y-2 p-8 text-center">
          <h3 className={T.cardTitleProse}>Billing is unavailable</h3>
          <p className={cn(T.helper, "mx-auto max-w-xl")}>
            {errorMessage(loadError, "Failed to load billing")}
          </p>
        </Card>
      ) : (
        <>
          {!summary ? (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
              <Skeleton className="h-32" />
              <Skeleton className="h-32" />
              <Skeleton className="h-32" />
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
              <KPITile
                label="Spend"
                value={money(total)}
                helper={
                  capped
                    ? `${pctOfBudget.toFixed(1)}% of budget`
                    : unlimited
                      ? "unlimited budget"
                      : "no budget configured"
                }
              />
              <KPITile
                label="Charges"
                value={<Count value={chargeCount} />}
                helper={`average ${money(moneyFromNanos(avgNanos, seriesUnit), seriesUnit)} per ${granularity}`}
              />
              <KPITile
                label="Budget"
                value={
                  capped ? (
                    <>
                      {money(remaining)}
                      <span className={cn(T.helper, "ml-1.5 text-sm")}>
                        left
                      </span>
                    </>
                  ) : unlimited ? (
                    "Unlimited"
                  ) : (
                    <span className={T.helper}>None</span>
                  )
                }
                helper={
                  <div className="space-y-2">
                    {capped ? (
                      <div
                        role="progressbar"
                        aria-label="Budget used"
                        aria-valuemin={0}
                        aria-valuemax={FULL_PERCENT}
                        aria-valuenow={Math.round(pctOfBudget)}
                        className="h-1.5 w-full rounded bg-muted"
                      >
                        <div
                          className={cn(
                            "h-full rounded transition-all",
                            pctOfBudget >= BUDGET_CRITICAL_PERCENT
                              ? "bg-destructive"
                              : pctOfBudget >= BUDGET_WARN_PERCENT
                                ? "bg-warning"
                                : "bg-success",
                          )}
                          style={{ width: `${pctOfBudget}%` }}
                        />
                      </div>
                    ) : null}
                    {tenantHandle ? (
                      <Link
                        href={tenantBudgetHref(tenantHandle)}
                        className="text-xs text-primary hover:underline"
                      >
                        {capped || unlimited
                          ? "Change the budget"
                          : "Set a budget"}
                      </Link>
                    ) : null}
                  </div>
                }
              />
            </div>
          )}

          <Card>
            <CardHeader className="flex flex-wrap items-center justify-between gap-3">
              <CardTitle
                level={3}
                className={T.cardTitleProse}
                icon={ChartBarIcon}
              >
                Spend over time
              </CardTitle>
              <Segmented
                label="Granularity"
                options={GRANULARITIES.map((g) => ({
                  ...g,
                  disabled: !allowed.includes(g.value),
                }))}
                value={granularity}
                onChange={setWanted}
              />
            </CardHeader>
            <CardContent className="space-y-3">
              {!seriesQuery.data ? (
                <Skeleton className="h-48" />
              ) : buckets.length === 0 ? (
                <div className={cn(T.helper, "py-8 text-center")}>
                  No spend for {tenantName} in this period.
                </div>
              ) : (
                <>
                  <SpendChart
                    points={points}
                    granularity={granularity}
                    formatValue={formatAxis}
                    formatPoint={(p) =>
                      money(p.spent as typeof total, seriesUnit)
                    }
                  />
                  <div className={cn(T.hint, "flex flex-wrap gap-x-6 gap-y-1")}>
                    {peak && peak.start ? (
                      <span>
                        Peak: {money(peak.spent, seriesUnit)} on{" "}
                        {formatDate(timestampDate(peak.start))}
                      </span>
                    ) : null}
                    <span>
                      Average:{" "}
                      {money(moneyFromNanos(avgNanos, seriesUnit), seriesUnit)}{" "}
                      per {granularity}
                    </span>
                  </div>
                </>
              )}
            </CardContent>
          </Card>

          {summary && chargeCount === 0 ? (
            <Card className="space-y-3 p-8 text-center">
              <h3 className={T.cardTitleProse}>No charges in this period</h3>
              <p className={cn(T.helper, "mx-auto max-w-xl")}>
                {tenantName} was not billed in this period. Charges accrue when
                its capability tokens call billable operations — presign, upload
                and the like.
              </p>
              {preset !== "90d" ? (
                <div>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setPreset("90d")}
                  >
                    Look at the last 90 days
                  </Button>
                </div>
              ) : null}
            </Card>
          ) : (
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
              <BreakdownCard
                title="Top capabilities"
                entries={summary?.topCapabilities ?? []}
                fallbackUnit={unit}
                renderLabel={(label) =>
                  tenantHandle ? (
                    <Link
                      href={`${tenantHref(tenantHandle)}/capabilities?id=${encodeURIComponent(label)}`}
                      className={cn(T.code, "hover:underline")}
                      title={label}
                    >
                      {shortLabel(label)}
                    </Link>
                  ) : (
                    <span className={T.code}>{shortLabel(label)}</span>
                  )
                }
                emptyHint="No capability charges in this period."
              />
              <BreakdownCard
                title="Top actors"
                entries={summary?.topActors ?? []}
                fallbackUnit={unit}
                renderLabel={(label) =>
                  tenantId ? (
                    <ActorName subject={label} tenantId={tenantId} />
                  ) : (
                    <span className={T.code}>{shortLabel(label)}</span>
                  )
                }
                emptyHint="No actor charges in this period."
              />
              <BreakdownCard
                title="Top operations"
                entries={summary?.topOps ?? []}
                fallbackUnit={unit}
                renderLabel={(label) => <span className={T.code}>{label}</span>}
                emptyHint="No operation charges in this period."
              />
            </div>
          )}
        </>
      )}
    </div>
  );
}

// A long identifier keeps its ends: enough to tell two apart.
const SHORT_LABEL_MAX = 24;
const SHORT_LABEL_HEAD = 8;
const SHORT_LABEL_TAIL = 6;
function shortLabel(label: string): string {
  return label.length > SHORT_LABEL_MAX
    ? `${label.slice(0, SHORT_LABEL_HEAD)}…${label.slice(-SHORT_LABEL_TAIL)}`
    : label || "—";
}

function Segmented<V extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { value: V; label: string; disabled?: boolean }[];
  value: V;
  onChange: (v: V) => void;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className="inline-flex rounded-md border bg-background p-0.5"
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          aria-pressed={value === o.value}
          disabled={o.disabled}
          title={o.disabled ? "Too many bars for this period" : undefined}
          onClick={() => onChange(o.value)}
          className={cn(
            "rounded px-3 py-1 text-sm transition-colors disabled:cursor-not-allowed disabled:opacity-40",
            value === o.value
              ? "bg-primary text-primary-foreground"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

function KPITile({
  label,
  value,
  helper,
}: {
  label: string;
  value: React.ReactNode;
  helper?: React.ReactNode;
}) {
  return (
    <Card className="space-y-2 p-4">
      <h3 className={T.label}>{label}</h3>
      <div className="text-2xl font-semibold tabular-nums">{value}</div>
      {helper && <div className={cn(T.helper, "text-xs")}>{helper}</div>}
    </Card>
  );
}

type BreakdownColumn = "label" | "spend" | "charges";
const BREAKDOWN_SORT: SortAccessors<TopEntry, BreakdownColumn> = {
  label: (e) => e.label || undefined,
  spend: (e) => moneyToNanos(e.spent),
  charges: (e) => e.chargeCount,
};

function BreakdownCard({
  title,
  entries,
  fallbackUnit,
  renderLabel,
  emptyHint,
}: {
  title: string;
  entries: TopEntry[];
  // Unit for an entry whose amount carries none.
  fallbackUnit: string;
  renderLabel: (label: string) => React.ReactNode;
  emptyHint: string;
}) {
  // A top-N list arrives ranked and short: it sorts, but has nothing to search.
  const { sort, toggleSort } = useTableSort<BreakdownColumn>();
  const rows = useMemo(
    () => sortRows(entries, sort, BREAKDOWN_SORT),
    [entries, sort],
  );
  const sortProps = { current: sort, onSort: toggleSort };
  return (
    <Card>
      <CardHeader>
        <CardTitle
          level={3}
          className={T.cardTitleProse}
          icon={TableCellsIcon}
          tone={IconTone.Neutral}
        >
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {entries.length === 0 ? (
          <div className={T.helper}>{emptyHint}</div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <SortableHead label="Label" column="label" {...sortProps} />
                <SortableHead
                  label="Spend"
                  column="spend"
                  align="end"
                  className="text-right"
                  {...sortProps}
                />
                <SortableHead
                  label="Charges"
                  column="charges"
                  align="end"
                  className="text-right"
                  {...sortProps}
                />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((e) => (
                <TableRow key={e.label}>
                  <TableCell className="max-w-48 truncate">
                    {renderLabel(e.label)}
                  </TableCell>
                  <TableCell numeric>
                    {formatMoney(e.spent, {
                      fallbackUnit,
                      fractionDigits: SPEND_DIGITS,
                    })}
                  </TableCell>
                  <TableCell numeric className="font-mono text-sm">
                    <Count value={e.chargeCount} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
