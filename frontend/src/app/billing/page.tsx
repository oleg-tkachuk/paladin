"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { ConnectError } from "@connectrpc/connect";
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
import { Sparkline } from "@/components/ui/Sparkline";
import { Select } from "@/components/ui/Select";
import { useNotification } from "@/components/ui/Notification";
import { useScope } from "@/context/ScopeContext";
import { billingClient } from "@/lib/connect/client";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import { formatMoney } from "@/lib/format/money";
import type {
  GetTenantSummaryResponse,
  GetTenantTimeSeriesResponse,
  TopEntry,
} from "@/gen/paladin/admin/v1/billing_service_pb";

// /billing — per-tenant capability spend dashboard. Reads the
// charges ledger (migration 027) via BillingService:
//
//   - Summary tile row (total, count, budget remaining)
//   - Time-series chart (Sparkline over date_trunc'd buckets)
//   - Three top-N breakdowns (capability_id, actor_subject, op)
//
// Period selector defaults to 30d (matches the backend default
// when period_start / period_end are unset). Custom range opens
// two date inputs — no fancy range picker primitive exists yet
// and that's fine for an MVP surface.

type Granularity = "hour" | "day" | "week";

type PresetKey = "7d" | "30d" | "90d" | "custom";

const PRESETS: Array<{ key: PresetKey; label: string; days: number | null }> = [
  { key: "7d", label: "7d", days: 7 },
  { key: "30d", label: "30d", days: 30 },
  { key: "90d", label: "90d", days: 90 },
  { key: "custom", label: "Custom", days: null },
];

// Compact number formatter for charge counts > 9999 — keeps the
// KPI tile from blowing out (e.g. 12.3k vs 12,345).
function formatCount(n: number | bigint): string {
  const v = typeof n === "bigint" ? Number(n) : n;
  if (v > 9999) {
    return new Intl.NumberFormat(undefined, {
      notation: "compact",
      maximumFractionDigits: 1,
    }).format(v);
  }
  return v.toLocaleString();
}

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
  unitCode,
  linkBuilder,
  emptyHint,
}: {
  title: string;
  entries: TopEntry[];
  unitCode: string;
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
              const spend = formatMoney(e.amount, unitCode);
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
                    {formatCount(e.chargeCount)}
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
  const { tenantId } = useScope();
  const { showNotification } = useNotification();

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
    } catch (err) {
      const msg =
        err instanceof ConnectError ? err.rawMessage : "Failed to load billing";
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
  const total = summary?.totalAmount ?? 0;
  const unit = summary?.unitCode ?? "USD";
  const max = summary?.maxBudgetAmount ?? 0;
  const chargeCount = summary?.chargeCount ? Number(summary.chargeCount) : 0;
  const remaining = Math.max(0, max - total);
  const pctOfBudget = max > 0 ? Math.min(100, (total / max) * 100) : 0;

  // Time-series derived values for the annotation under the chart.
  const buckets = timeseries?.buckets ?? [];
  const tsUnit = timeseries?.unitCode || unit;
  const tsValues = useMemo(() => buckets.map((b) => b.amount), [buckets]);
  const peak = useMemo(() => {
    if (buckets.length === 0) return null;
    return buckets.reduce(
      (acc, b) => (b.amount > acc.amount ? b : acc),
      buckets[0],
    );
  }, [buckets]);
  const avg = useMemo(() => {
    if (buckets.length === 0) return 0;
    const sum = buckets.reduce((acc, b) => acc + b.amount, 0);
    return sum / buckets.length;
  }, [buckets]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Billing"
        description="Per-tenant capability spend over the charges ledger."
      />

      {/* Period selector */}
      <Card className="flex flex-wrap items-center gap-3 p-4">
        <div className={T.label}>Period</div>
        <div className="inline-flex rounded-md border bg-background p-0.5">
          {PRESETS.map((p) => (
            <button
              key={p.key}
              type="button"
              onClick={() => setPreset(p.key)}
              className={cn(
                "px-3 py-1 text-sm transition-colors rounded",
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
          <>
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
          </>
        )}
        <div className="ml-auto">
          <Button variant="outline" size="sm" onClick={() => void fetchAll()}>
            Refresh
          </Button>
        </div>
      </Card>

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
            value={formatMoney(total, unit, undefined, 4)}
            helper={
              max > 0
                ? `${pctOfBudget.toFixed(1)}% of budget`
                : "no budget configured"
            }
          />
          <KPITile
            label="Charges"
            value={formatCount(chargeCount)}
            helper="this period"
          />
          <KPITile
            label="Budget remaining"
            value={
              max > 0 ? (
                formatMoney(remaining, unit)
              ) : (
                <span className={T.helper}>—</span>
              )
            }
            helper={
              max > 0 ? (
                <div className="h-1.5 w-full rounded bg-muted">
                  <div
                    className={cn(
                      "h-full rounded transition-all",
                      pctOfBudget >= 90
                        ? "bg-destructive"
                        : pctOfBudget >= 70
                          ? "bg-amber-500"
                          : "bg-emerald-500",
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
          <Skeleton className="h-32" />
        ) : buckets.length === 0 ? (
          <div className={cn(T.helper, "py-8 text-center")}>
            No data for the selected period.
          </div>
        ) : (
          <>
            <Sparkline data={tsValues} className="h-32" />
            <div className={cn(T.hint, "flex flex-wrap gap-x-6 gap-y-1")}>
              {peak && (
                <span>
                  Peak: {formatMoney(peak.amount, tsUnit)} on{" "}
                  {tsToDate(peak.start)?.toISOString().slice(0, 10) ?? "—"}
                </span>
              )}
              <span>
                Average: {formatMoney(avg, tsUnit)} per {granularity}
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
            No charges recorded for this tenant in the selected period. Charges
            accumulate when capability tokens are used to call billable handlers
            (presign / upload / etc.).
          </p>
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <BreakdownCard
            title="Top capabilities"
            entries={summary?.topCapabilities ?? []}
            unitCode={unit}
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
            unitCode={unit}
            emptyHint="No actor charges in this period."
          />
          <BreakdownCard
            title="Top ops"
            entries={summary?.topOps ?? []}
            unitCode={unit}
            emptyHint="No op charges in this period."
          />
        </div>
      )}
    </div>
  );
}
