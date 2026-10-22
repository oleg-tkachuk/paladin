"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { ArrowPathIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { Count } from "@/components/ui/Count";
import { Skeleton } from "@/components/ui/Skeleton";
import { ActorName } from "@/components/features/audit/ActorName";
import type { Granularity } from "@/components/features/billing/spendSeries";
import { errorMessage } from "@/hooks/errorContract";
import { usePlatformCurrency } from "@/hooks/usePlatformCurrency";
import { billingClient } from "@/lib/connect/client";
import { normalizeError } from "@/lib/connect/error";
import {
  budgetLimit,
  budgetUnit,
  budgetUnlimited,
} from "@/lib/format/budgetCeiling";
import {
  ABSTRACT_UNIT_CODE,
  formatMoney,
  moneyFromNanos,
  moneyPercent,
  moneyToNanos,
} from "@/lib/format/money";
import { tenantBudgetHref, tenantHref } from "@/lib/navigation/hrefs";
import { BILLING_QUERY_KEY } from "@/lib/queries/billing";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { BillingPeriodBar } from "./BillingPeriodBar";
import { KPI_TILE_HEIGHT, KPITile } from "./KPITile";
import { SpendOverTimeCard } from "./SpendOverTimeCard";
import { spendStats } from "./spendStats";
import { allowedGranularities, pickGranularity } from "./period";
import { RankedSpendCard } from "./RankedSpendCard";
import type { BillingPeriodState } from "./useBillingPeriod";

// Spend is shown to four decimals so a sub-cent charge does not read as
// nothing.
const SPEND_DIGITS = 4;
// A budget this far used is close; this far, all but gone.
const BUDGET_WARN_PERCENT = 70;
const BUDGET_CRITICAL_PERCENT = 90;
const FULL_PERCENT = 100;
// The period the empty state offers when the chosen one held no charge.
const WIDER_PRESET = "90d";

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

  const stats = useMemo(
    () =>
      spendStats(
        seriesQuery.data?.buckets ?? [],
        period ?? undefined,
        granularity,
      ),
    [seriesQuery.data, period, granularity],
  );
  const seriesUnit = stats.unit || unit;
  const avgNanos = stats.avgNanos;
  const money = (m: typeof total, fallbackUnit = unit) =>
    formatMoney(m, { fallbackUnit, fractionDigits: SPEND_DIGITS });
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

          <SpendOverTimeCard
            buckets={seriesQuery.data?.buckets}
            stats={stats}
            unit={seriesUnit}
            granularity={granularity}
            allowed={allowed}
            onGranularity={setWanted}
            emptyText={`No spend for ${tenantName} in this period.`}
            error={seriesQuery.error}
            onRetry={() => void seriesQuery.refetch()}
          />

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
