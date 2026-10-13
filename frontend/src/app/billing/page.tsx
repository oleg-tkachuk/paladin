"use client";

import {
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useQuery } from "@tanstack/react-query";
import { Timestamp } from "@bufbuild/protobuf/wkt";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Select } from "@/components/ui/Select";
import { SpendChart } from "@/components/features/billing/SpendChart";
import {
  fillSeries,
  type Granularity,
} from "@/components/features/billing/spendSeries";
import { useTenants } from "@/hooks/useTenants";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { useNotification } from "@/components/ui/Notification";
import { useScope } from "@/context/ScopeContext";
import { billingClient } from "@/lib/connect/client";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import {
  formatMoney,
  moneyFromNanos,
  moneyPercent,
  moneyToNanos,
  moneyToNumber,
  unitOf,
} from "@/lib/format/money";
import {
  budgetLimit,
  budgetUnit,
  budgetUnlimited,
} from "@/lib/format/budgetCeiling";
import type {
  GetTenantSummaryResponse,
  GetTenantTimeSeriesResponse,
  TopEntry,
} from "@/gen/paladin/admin/v1/billing_service_pb";
import { formatCompactCount, formatDate } from "@/lib/format/locale";
import { errorMessage } from "@/hooks/errorContract";

// /billing — per-tenant capability spend dashboard. Reads the
// charges ledger (`001_initial_schema.sql`) via BillingService:
//
//   - Tenant picker: the ledger is per tenant, and the console's own scope
//     is usually the platform tenant, which is billed nothing — the page used
//     to show its empty ledger with no way to look at anyone else's. The
//     choice lives in ?tenant= so a link opens the same view.
//   - Summary tile row (total, count, budget remaining)
//   - Time-series chart (bars on an even time axis, empty buckets included)
//   - Three top-N breakdowns (capability_id, actor_subject, op)
//
// Period selector defaults to 30d (matches the backend default
// when period_start / period_end are unset). Custom range opens
// two date inputs — no fancy range picker primitive exists yet
// and that's fine for an MVP surface.

// The query parameter that names the tenant on view.
const TENANT_PARAM = "tenant";

// Money's whole unit in nanos — the chart works in numbers of whole units.
const NANOS_PER_UNIT = 1e9;

// Total spend is shown to four decimals so a sub-cent charge does not read
// as nothing.
const SPEND_DIGITS = 4;

type PresetKey = "7d" | "30d" | "90d" | "custom";

const PRESETS: Array<{ key: PresetKey; label: string; days: number | null }> = [
  { key: "7d", label: "7d", days: 7 },
  { key: "30d", label: "30d", days: 30 },
  { key: "90d", label: "90d", days: 90 },
  { key: "custom", label: "Custom", days: null },
];

function tsToDate(ts: Timestamp | undefined): Date | null {
  if (!ts) return null;
  const ms = Number(ts.seconds) * 1000 + Math.floor(Number(ts.nanos) / 1e6);
  if (!Number.isFinite(ms) || ms === 0) return null;
  return new Date(ms);
}

function dateToTimestamp(d: Date): Timestamp {
  const ms = d.getTime();
  return {
    $typeName: "google.protobuf.Timestamp",
    seconds: BigInt(Math.floor(ms / 1000)),
    nanos: (ms % 1000) * 1_000_000,
  } as Timestamp;
}

// Derived period — preset + custom dates → concrete (start, end) pair.
// Returns undefined for fields the backend should default (e.g. "30d"
// preset returns both undefined, letting the server pick its 30d
// default; custom may return one or both).
function resolvePeriod(
  preset: PresetKey,
  customStart: string,
  customEnd: string,
): { start?: Timestamp; end?: Timestamp } {
  if (preset === "custom") {
    const out: { start?: Timestamp; end?: Timestamp } = {};
    if (customStart) {
      const d = new Date(customStart + "T00:00:00Z");
      if (!Number.isNaN(d.getTime())) out.start = dateToTimestamp(d);
    }
    if (customEnd) {
      const d = new Date(customEnd + "T23:59:59Z");
      if (!Number.isNaN(d.getTime())) out.end = dateToTimestamp(d);
    }
    return out;
  }
  const days = PRESETS.find((p) => p.key === preset)?.days ?? 30;
  const end = new Date();
  const start = new Date(end.getTime() - days * 24 * 60 * 60 * 1000);
  return { start: dateToTimestamp(start), end: dateToTimestamp(end) };
}

// Small primitives so the page reads as a layout file rather than a
// pile of div soup. Each KPI tile carries label + big value + helper.
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

// Top-N breakdown card. linkPrefix optional — when set, each row
// becomes a Link to /capabilities?id=<label> etc. (used for the
// capability_id breakdown). Truncates the label to 16 mono chars
// when it looks like a UUID so the column doesn't dominate.
function BreakdownCard({
  title,
  entries,
  fallbackUnit,
  linkBuilder,
  emptyHint,
}: {
  title: string;
  entries: TopEntry[];
  // Unit for an entry whose amount carries none.
  fallbackUnit: string;
  linkBuilder?: (label: string) => string;
  emptyHint: string;
}) {
  return (
    <Card className="space-y-3 p-4">
      <h3 className={T.cardTitleProse}>{title}</h3>
      {entries.length === 0 ? (
        <div className={T.helper}>{emptyHint}</div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Label</TableHead>
              <TableHead className="text-right">Spend</TableHead>
              <TableHead className="text-right">Charges</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.map((e) => {
              const label = e.label || "—";
              const display =
                label.length > 24
                  ? `${label.slice(0, 8)}…${label.slice(-6)}`
                  : label;
              const spend = formatMoney(e.spent, {
                fallbackUnit,
                fractionDigits: SPEND_DIGITS,
              });
              const cell = linkBuilder ? (
                <Link
                  href={linkBuilder(label)}
                  className={cn(T.code, "hover:underline")}
                >
                  {display}
                </Link>
              ) : (
                <span className={T.code}>{display}</span>
              );
              return (
                <TableRow key={label}>
                  <TableCell>{cell}</TableCell>
                  <TableCell className="text-right tabular-nums">
                    {spend}
                  </TableCell>
                  <TableCell className="text-right font-mono text-sm tabular-nums">
                    {formatCompactCount(e.chargeCount)}
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </Card>
  );
}

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
  const { showNotification } = useNotification();
  const router = useRouter();
  const search = useSearchParams();
  const tenantId = search.get(TENANT_PARAM) || scopeTenantId || undefined;
  // fetchTenants is state-only: a failed list comes back empty and the reason
  // is the hook's `error`.
  const { fetchTenants, error: tenantsError } = useTenants();

  // Trashed tenants too: their charges stay on the ledger.
  const tenantsQuery = useQuery({
    queryKey: ["billingTenants"],
    queryFn: () =>
      fetchTenants("", "", { includeTrashed: true }).then((r) => r.tenants),
  });
  const tenantOptions = useMemo(
    () =>
      (tenantsQuery.data ?? [])
        .map((t) => ({
          value: t.tenantId,
          label: `${t.displayName || t.slug}${t.deletedAt ? " · in trash" : ""}`,
        }))
        .sort((a, b) => a.label.localeCompare(b.label)),
    [tenantsQuery.data],
  );
  const tenantName =
    tenantOptions.find((o) => o.value === tenantId)?.label ?? "this tenant";
  const selectTenant = (id: string) => {
    const next = new URLSearchParams(search.toString());
    next.set(TENANT_PARAM, id);
    router.replace(`?${next.toString()}`);
  };

  // ── period state ────────────────────────────────────────────────────
  const [preset, setPreset] = useState<PresetKey>("30d");
  const [customStart, setCustomStart] = useState("");
  const [customEnd, setCustomEnd] = useState("");
  const [granularity, setGranularity] = useState<Granularity>("day");

  // ── data state ──────────────────────────────────────────────────────
  const [summary, setSummary] = useState<GetTenantSummaryResponse | null>(null);
  const [timeseries, setTimeseries] =
    useState<GetTenantTimeSeriesResponse | null>(null);
  // `loading` was used by the skeleton conditions but they switched
  // to `!summary` / `!timeseries` (no flash between mount and first
  // fetch). Keep the setter — the in-flight flag may still be useful
  // for "Refresh" button affordance later.
  const [, setLoading] = useState(false);
  // Why the last load failed. Without it a failed first load left the chart
  // a skeleton forever and the breakdowns saying "no charges", which is a
  // claim about data the page never received.
  const [loadError, setLoadError] = useState<string | null>(null);

  // Debounce period changes — date pickers fire on each keystroke,
  // we don't want one RPC per character.
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const fetchAll = useCallback(async () => {
    if (!tenantId) return;
    const period = resolvePeriod(preset, customStart, customEnd);
    setLoading(true);
    try {
      const [s, ts] = await Promise.all([
        billingClient.getTenantSummary({
          tenantId,
          periodStart: period.start,
          periodEnd: period.end,
        }),
        billingClient.getTenantTimeSeries({
          tenantId,
          periodStart: period.start,
          periodEnd: period.end,
          granularity,
        }),
      ]);
      setSummary(s);
      setTimeseries(ts);
      setLoadError(null);
    } catch (err) {
      const msg = errorMessage(err, "Failed to load billing");
      setLoadError(msg);
      showNotification({ type: "error", title: "Load failed", message: msg });
    } finally {
      setLoading(false);
    }
  }, [tenantId, preset, customStart, customEnd, granularity, showNotification]);

  useEffect(() => {
    // Debounce only after the first successful load — without this
    // every navigation INTO /billing waited 300ms before firing the
    // fetch (blank cards visible during the wait, then a skeleton
    // burst, then data; three visual states). The debounce IS still
    // useful for the date pickers — they fire the effect on each
    // keystroke. So: 0ms when summary is missing (initial load),
    // 300ms otherwise.
    const delay = summary ? 300 : 0;
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      void fetchAll();
    }, delay);
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
    };
    // summary intentionally not in the dep list — only the FIRST
    // load needs the no-delay path; once we have data the 300ms
    // debounce kicks in for subsequent changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fetchAll]);

  // Derived KPI stats. Pulled from the summary; safe to compute
  // even when the response is sparse — formatters handle 0.
  const total = summary?.total;
  // Default to XXX (no currency) instead of "USD" when the summary
  // doesn't carry a unit yet — operators using non-currency metering
  // or freshly-created tenants don't see a misleading dollar sign on
  // a zero total.
  const unit =
    total?.currencyCode ||
    budgetUnit(summary?.budgetCeiling) ||
    unitOf(undefined);
  // Unset with no budget row; a cap of zero is a cap.
  const max = budgetLimit(summary?.budgetCeiling);
  const capped = max !== undefined;
  const unlimitedBudget = budgetUnlimited(summary?.budgetCeiling);
  const chargeCount = summary?.chargeCount ? Number(summary.chargeCount) : 0;
  const remainingNanos = moneyToNanos(max) - moneyToNanos(total);
  const remaining = moneyFromNanos(
    remainingNanos > 0n ? remainingNanos : 0n,
    unit,
  );
  // A cap of zero is reached before anything is spent.
  const pctOfBudget = capped
    ? Math.min(100, moneyPercent(total, max) ?? 100)
    : 0;

  // Time-series derived values for the annotation under the chart.
  //
  // The `?? []` fallback has to be memoized or the three useMemo hooks below
  // are decorative: on every render with no time series it produces a fresh
  // array, their dep changes, and each one recomputes. Memoizing on
  // `timeseries` gives the empty case one stable array for as long as it
  // stays empty, which is what the hooks were written assuming.
  const buckets = useMemo(() => timeseries?.buckets ?? [], [timeseries]);
  // Each bucket carries its own unit; the summary's is the fallback.
  const tsUnit =
    buckets.find((b) => b.spent?.currencyCode)?.spent?.currencyCode || unit;
  // The chart takes numbers; exactness ends at the display edge. The axis
  // covers the whole period, so a quiet day reads as a gap, not as nothing.
  const points = useMemo(() => {
    const raw = buckets.flatMap((b) => {
      const start = tsToDate(b.start);
      return start
        ? [
            {
              start,
              value: moneyToNumber(b.spent),
              chargeCount: Number(b.chargeCount),
              spent: b.spent,
            },
          ]
        : [];
    });
    const period = resolvePeriod(preset, customStart, customEnd);
    const from = tsToDate(period.start) ?? raw[0]?.start;
    const to = tsToDate(period.end) ?? raw.at(-1)?.start ?? new Date();
    return from ? fillSeries(raw, from, to, granularity) : raw;
  }, [buckets, preset, customStart, customEnd, granularity]);
  const formatAxis = useCallback(
    (v: number) =>
      formatMoney(
        moneyFromNanos(BigInt(Math.round(v * NANOS_PER_UNIT)), tsUnit),
      ),
    [tsUnit],
  );
  const peak = useMemo(() => {
    if (buckets.length === 0) return null;
    return buckets.reduce(
      (acc, b) => (moneyToNanos(b.spent) > moneyToNanos(acc.spent) ? b : acc),
      buckets[0],
    );
  }, [buckets]);
  // Summed in exact nanos; the per-bucket mean truncates below a nano. Over
  // every bucket of the period, quiet ones included — averaging only the
  // buckets that billed something overstated the typical day.
  const avgNanos = useMemo(() => {
    if (points.length === 0) return 0n;
    const sum = buckets.reduce((acc, b) => acc + moneyToNanos(b.spent), 0n);
    return sum / BigInt(points.length);
  }, [buckets, points]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Billing"
        description="Capability spend from the charges ledger, one tenant at a time."
      />

      {/* Tenant, period and refresh: one row of controls above the data. */}
      <div className="flex flex-wrap items-center gap-3">
        <Select
          aria-label="Tenant"
          value={tenantId}
          onChange={selectTenant}
          options={tenantOptions}
          placeholder="Choose a tenant"
          // A long tenant name ellipsises inside the trigger instead of
          // running under the period control beside it.
          className="w-64 min-w-0 [&_[data-slot=select-value]]:truncate"
        />
        <div
          role="group"
          aria-label="Period"
          className="inline-flex rounded-md border bg-background p-0.5"
        >
          {PRESETS.map((p) => (
            <button
              key={p.key}
              type="button"
              aria-pressed={preset === p.key}
              onClick={() => setPreset(p.key)}
              className={cn(
                "rounded px-3 py-1 text-sm transition-colors",
                preset === p.key
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {p.label}
            </button>
          ))}
        </div>
        {preset === "custom" && (
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
        )}
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          onClick={() => void fetchAll()}
        >
          Refresh
        </Button>
      </div>
      {tenantsError && (
        <ListLoadError
          variant="inline"
          what="Tenants"
          reason={tenantsError}
          onRetry={() => void tenantsQuery.refetch()}
        />
      )}

      {loadError !== null && !summary ? (
        <Card className="space-y-2 p-8 text-center">
          <h3 className={T.cardTitleProse}>Billing is unavailable</h3>
          <p className={cn(T.helper, "mx-auto max-w-xl")}>{loadError}</p>
        </Card>
      ) : (
        <>
          {/* KPI tiles. Show skeleton whenever we don't have data yet —
          covers both "first fetch in flight" and "haven't started
          fetching" (tenantId still resolving). The previous
          condition `loading && !summary` left a brief flash of
          empty tiles between mount and the first fetch firing. */}
          {!summary ? (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
              <Skeleton className="h-28" />
              <Skeleton className="h-28" />
              <Skeleton className="h-28" />
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
              <KPITile
                label="Total spend"
                value={formatMoney(total, {
                  fallbackUnit: unit,
                  fractionDigits: SPEND_DIGITS,
                })}
                helper={
                  capped
                    ? `${pctOfBudget.toFixed(1)}% of budget`
                    : unlimitedBudget
                      ? "unlimited budget"
                      : "no budget configured"
                }
              />
              <KPITile
                label="Charges"
                value={formatCompactCount(chargeCount)}
                helper="this period"
              />
              <KPITile
                label="Budget remaining"
                value={
                  capped ? (
                    formatMoney(remaining)
                  ) : (
                    <span className={T.helper}>—</span>
                  )
                }
                helper={
                  capped ? (
                    <div className="h-1.5 w-full rounded bg-muted">
                      <div
                        className={cn(
                          "h-full rounded transition-all",
                          pctOfBudget >= 90
                            ? "bg-destructive"
                            : pctOfBudget >= 70
                              ? "bg-warning"
                              : "bg-success",
                        )}
                        style={{ width: `${pctOfBudget}%` }}
                      />
                    </div>
                  ) : (
                    "set a tenant budget to enable progress tracking"
                  )
                }
              />
            </div>
          )}

          {/* Time-series */}
          <Card className="space-y-3 p-4">
            <div className="flex items-center justify-between gap-3">
              <h3 className={T.cardTitleProse}>Spend over time</h3>
              <div className="flex items-center gap-2">
                <span className={T.label}>Granularity</span>
                <Select
                  aria-label="Granularity"
                  value={granularity}
                  onChange={(v) => setGranularity(v as Granularity)}
                  options={[
                    { value: "hour", label: "Hour" },
                    { value: "day", label: "Day" },
                    { value: "week", label: "Week" },
                  ]}
                />
              </div>
            </div>
            {!timeseries ? (
              <Skeleton className="h-48" />
            ) : buckets.length === 0 ? (
              <div className={cn(T.helper, "py-8 text-center")}>
                No spend for {tenantName} in the selected period.
              </div>
            ) : (
              <>
                <SpendChart
                  points={points}
                  granularity={granularity}
                  formatValue={formatAxis}
                  formatPoint={(p) =>
                    formatMoney(p.spent as typeof total, {
                      fallbackUnit: tsUnit,
                      fractionDigits: SPEND_DIGITS,
                    })
                  }
                />
                <div className={cn(T.hint, "flex flex-wrap gap-x-6 gap-y-1")}>
                  {peak && (
                    <span>
                      Peak:{" "}
                      {formatMoney(peak.spent, {
                        fallbackUnit: tsUnit,
                        fractionDigits: SPEND_DIGITS,
                      })}{" "}
                      on{" "}
                      {(() => {
                        const d = tsToDate(peak.start);
                        return d ? formatDate(d) : "—";
                      })()}
                    </span>
                  )}
                  <span>
                    Average:{" "}
                    {formatMoney(moneyFromNanos(avgNanos, tsUnit), {
                      fractionDigits: SPEND_DIGITS,
                    })}{" "}
                    per {granularity}
                  </span>
                </div>
              </>
            )}
          </Card>

          {/* Empty state — when the period genuinely has no charges, hide
          the breakdowns and show a single explanation card. */}
          {summary && chargeCount === 0 ? (
            <Card className="space-y-2 p-8 text-center">
              <h3 className={T.cardTitleProse}>No charges in this period</h3>
              <p className={cn(T.helper, "max-w-xl mx-auto")}>
                {tenantName} was not billed in the selected period. Charges
                accumulate when its capability tokens call billable handlers
                (presign, upload and the like). Choose another tenant or a
                longer period above.
              </p>
            </Card>
          ) : (
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
              <BreakdownCard
                title="Top capabilities"
                entries={summary?.topCapabilities ?? []}
                fallbackUnit={unit}
                // Capabilities are tenant-scoped now — link goes to
                // the active tenant's Capabilities tab. Falls back to a
                // dead anchor when scope is empty (shouldn't happen on
                // the billing page, which already requires a tenant).
                linkBuilder={
                  tenantId
                    ? (id) =>
                        `/tenants/${encodeURIComponent(tenantId)}/capabilities?id=${encodeURIComponent(id)}`
                    : undefined
                }
                emptyHint="No capability charges in this period."
              />
              <BreakdownCard
                title="Top actors"
                entries={summary?.topActors ?? []}
                fallbackUnit={unit}
                emptyHint="No actor charges in this period."
              />
              <BreakdownCard
                title="Top ops"
                entries={summary?.topOps ?? []}
                fallbackUnit={unit}
                emptyHint="No op charges in this period."
              />
            </div>
          )}
        </>
      )}
    </div>
  );
}
