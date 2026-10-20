"use client";

import { useCallback, useMemo, useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { ArrowPathIcon, ChartBarIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Count } from "@/components/ui/Count";
import { Skeleton } from "@/components/ui/Skeleton";
import { ActorName } from "@/components/features/audit/ActorName";
import { SpendChart } from "@/components/features/billing/SpendChart";
import {
  fillSeries,
  type Granularity,
} from "@/components/features/billing/spendSeries";
import { errorMessage } from "@/hooks/errorContract";
import { usePlatformCurrency } from "@/hooks/usePlatformCurrency";
import { billingClient } from "@/lib/connect/client";
import { normalizeError } from "@/lib/connect/error";
import {
  budgetLimit,
  budgetUnit,
  budgetUnlimited,
} from "@/lib/format/budgetCeiling";
import { formatDate } from "@/lib/format/locale";
import {
  ABSTRACT_UNIT_CODE,
  formatMoney,
  moneyFromNanos,
  moneyPercent,
  moneyToNanos,
  moneyToNumber,
} from "@/lib/format/money";
import { tenantBudgetHref, tenantHref } from "@/lib/navigation/hrefs";
import { BILLING_QUERY_KEY } from "@/lib/queries/billing";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { BillingPeriodBar } from "./BillingPeriodBar";
import { GRANULARITIES, allowedGranularities, pickGranularity } from "./period";
import { RankedSpendCard } from "./RankedSpendCard";
import { Segmented } from "./Segmented";
import type { BillingPeriodState } from "./useBillingPeriod";

// Spend is shown to four decimals so a sub-cent charge does not read as
// nothing.
const SPEND_DIGITS = 4;
// A budget this far used is close; this far, all but gone.
const BUDGET_WARN_PERCENT = 70;
const BUDGET_CRITICAL_PERCENT = 90;
const FULL_PERCENT = 100;
// The chart takes numbers of whole units.
const NANOS_PER_UNIT = 1e9;
// The period the empty state offers when the chosen one held no charge.
const WIDER_PRESET = "90d";
// The height a KPI tile takes, so its placeholder keeps the same place.
const KPI_TILE_HEIGHT = "h-18";

// A long identifier keeps its ends: enough to tell two apart.
const SHORT_LABEL_MAX = 24;
const SHORT_LABEL_HEAD = 8;
const SHORT_LABEL_TAIL = 6;
function shortLabel(label: string): string {
  return label.length > SHORT_LABEL_MAX
    ? `${label.slice(0, SHORT_LABEL_HEAD)}…${label.slice(-SHORT_LABEL_TAIL)}`
    : label || "—";
}

/**
 * One tenant's capability spend over the period, from the charges ledger:
 * what it came to against its budget, how it moved over time, and which
 * capabilities, actors and operations it went to.
 */
export function TenantBilling({
  tenantId,
  tenantHandle,
  tenantName,
  state,
}: {
  tenantId: string;
  /** The slug the tenant's pages are addressed by. */
  tenantHandle: string;
  tenantName: string;
  state: BillingPeriodState;
}) {
  const platformCurrency = usePlatformCurrency();
  const { period, range } = state;
  const [wanted, setWanted] = useState<Granularity>("day");
  const granularity = period ? pickGranularity(period, wanted) : wanted;
  const allowed = period ? allowedGranularities(period) : [];

  const key = [tenantId, period?.start.getTime(), period?.end.getTime()];
  const summaryQuery = useQuery({
    queryKey: [...BILLING_QUERY_KEY, "summary", ...key],
    enabled: Boolean(range),
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
    queryKey: [...BILLING_QUERY_KEY, "series", ...key, granularity],
    enabled: Boolean(range),
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
  const totalNanos = moneyToNanos(total);
  const remainingNanos = moneyToNanos(max) - totalNanos;
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
      <div className="flex flex-wrap items-center justify-between gap-3">
        <BillingPeriodBar state={state} />
        <Button
          variant="outline"
          size="sm"
          onClick={state.refresh}
          disabled={refreshing || !range}
        >
          <ArrowPathIcon
            className={cn("size-4", refreshing && "animate-spin")}
          />
          Refresh
        </Button>
      </div>

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
              <Skeleton className={KPI_TILE_HEIGHT} />
              <Skeleton className={KPI_TILE_HEIGHT} />
              <Skeleton className={KPI_TILE_HEIGHT} />
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
                    money(remaining)
                  ) : unlimited ? (
                    "Unlimited"
                  ) : (
                    <span className={T.helper}>None</span>
                  )
                }
                helper={capped ? "left" : undefined}
                action={
                  <Link
                    href={tenantBudgetHref(tenantHandle)}
                    className="text-xs text-primary hover:underline"
                  >
                    {capped || unlimited ? "Change the budget" : "Set a budget"}
                  </Link>
                }
                footer={
                  capped ? (
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
                  ) : null
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
                  title: "Too many bars for this period",
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
              {state.choice.preset !== WIDER_PRESET ? (
                <div>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => state.setPreset(WIDER_PRESET)}
                  >
                    Look at the last 90 days
                  </Button>
                </div>
              ) : null}
            </Card>
          ) : (
            <div className="grid grid-cols-1 items-start gap-4 md:grid-cols-2 xl:grid-cols-3">
              <RankedSpendCard
                title="Top capabilities"
                entries={summary?.topCapabilities ?? []}
                totalNanos={totalNanos}
                fallbackUnit={unit}
                renderLabel={(label) => (
                  <Link
                    href={`${tenantHref(tenantHandle)}/capabilities?id=${encodeURIComponent(label)}`}
                    className={cn(T.code, "hover:underline")}
                    title={label}
                  >
                    {shortLabel(label)}
                  </Link>
                )}
                emptyHint="No capability charges in this period."
              />
              <RankedSpendCard
                title="Top actors"
                entries={summary?.topActors ?? []}
                totalNanos={totalNanos}
                fallbackUnit={unit}
                renderLabel={(label) => (
                  <ActorName subject={label} tenantId={tenantId} />
                )}
                emptyHint="No actor charges in this period."
              />
              <RankedSpendCard
                title="Top operations"
                entries={summary?.topOps ?? []}
                totalNanos={totalNanos}
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

/**
 * A headline figure: its label, with the tile's one action beside it, over
 * the value and what qualifies it on one line. The card's own gap and
 * padding are tightened so a row of these stays a band, not a block.
 */
function KPITile({
  label,
  value,
  helper,
  action,
  footer,
}: {
  label: string;
  value: React.ReactNode;
  helper?: React.ReactNode;
  action?: React.ReactNode;
  footer?: React.ReactNode;
}) {
  return (
    <Card className="gap-1.5 px-4 py-3" aria-label={label} role="group">
      <div className="flex items-baseline justify-between gap-2">
        <h3 className={T.label}>{label}</h3>
        {action}
      </div>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <span className="text-xl font-semibold leading-tight tabular-nums">
          {value}
        </span>
        {helper && <span className={T.hint}>{helper}</span>}
      </div>
      {footer}
    </Card>
  );
}
