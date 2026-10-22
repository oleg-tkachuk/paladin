"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowPathIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Count } from "@/components/ui/Count";
import { Skeleton } from "@/components/ui/Skeleton";
import { usePlatformCurrency } from "@/hooks/usePlatformCurrency";
import { ABSTRACT_UNIT_CODE, formatMoney } from "@/lib/format/money";
import { platformSeriesQuery, tenantSpendQuery } from "@/lib/queries/billing";
import { cn } from "@/lib/utils";

import { BillingPeriodBar } from "./BillingPeriodBar";
import { KPI_TILE_HEIGHT, KPITile } from "./KPITile";
import { allowedGranularities, pickGranularity } from "./period";
import { platformTotals } from "./platformSpend";
import { SpendOverTimeCard } from "./SpendOverTimeCard";
import type { Granularity } from "./spendSeries";
import { spendStats } from "./spendStats";
import { TenantSpendCard } from "./TenantSpendCard";
import type { BillingPeriodState } from "./useBillingPeriod";

// Spend is shown to four decimals so a sub-cent charge does not read as
// nothing, as the rest of billing shows it.
const SPEND_DIGITS = 4;
// The headline figures, a placeholder each while they load.
const KPI_TILES = 3;

/**
 * Every tenant's capability spend over the period, from the charges ledger:
 * what the platform was charged in all, how it moved over time, and which
 * tenants it came from, each leading to its own billing.
 */
export function PlatformBilling({
  state,
  hrefOf,
}: {
  state: BillingPeriodState;
  /** Where a tenant's row leads, by its slug, or its id when it has none. */
  hrefOf: (tenant: string) => string;
}) {
  const platformCurrency = usePlatformCurrency() || ABSTRACT_UNIT_CODE;
  const { period, range } = state;
  const [wanted, setWanted] = useState<Granularity>("day");
  const granularity = period ? pickGranularity(period, wanted) : wanted;
  const allowed = period ? allowedGranularities(period) : [];

  const spendQuery = useQuery({
    ...tenantSpendQuery(range!),
    enabled: Boolean(range),
  });
  const seriesQuery = useQuery({
    ...platformSeriesQuery(range!, granularity),
    enabled: Boolean(range),
  });
  const tenants = spendQuery.data?.tenants;
  const totals = useMemo(
    () => (tenants ? platformTotals(tenants, platformCurrency) : null),
    [tenants, platformCurrency],
  );
  const stats = useMemo(
    () =>
      spendStats(
        seriesQuery.data?.buckets ?? [],
        period ?? undefined,
        granularity,
      ),
    [seriesQuery.data, period, granularity],
  );
  const unit = stats.unit || totals?.total.currencyCode || platformCurrency;
  const refreshing = spendQuery.isFetching || seriesQuery.isFetching;
  const money = (m: Parameters<typeof formatMoney>[0]) =>
    formatMoney(m, { fallbackUnit: unit, fractionDigits: SPEND_DIGITS });

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

      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {!totals ? (
          Array.from({ length: KPI_TILES }, (_, i) => (
            <Skeleton key={i} className={KPI_TILE_HEIGHT} />
          ))
        ) : (
          <>
            <KPITile
              label="Spend"
              value={money(totals.total)}
              helper={
                totals.otherUnit > 0
                  ? `and ${totals.otherUnit} tenant${totals.otherUnit === 1 ? "" : "s"} in other units`
                  : "every tenant, net of refunds"
              }
            />
            <KPITile
              label="Charges"
              value={<Count value={totals.charges} />}
              helper={
                totals.charges > 0
                  ? `average ${money(totals.perCharge)} per charge`
                  : undefined
              }
            />
            <KPITile
              label="Tenants"
              value={<Count value={totals.tenants} />}
              helper={
                spendQuery.data?.truncated
                  ? "that spent most — more spent"
                  : "that spent anything"
              }
            />
          </>
        )}
      </div>

      <SpendOverTimeCard
        buckets={seriesQuery.data?.buckets}
        stats={stats}
        unit={unit}
        granularity={granularity}
        allowed={allowed}
        onGranularity={setWanted}
        emptyText="Nothing was charged in this period."
        error={seriesQuery.error}
        onRetry={() => void seriesQuery.refetch()}
      />

      {range ? (
        <TenantSpendCard period={range} hrefOf={hrefOf} totals={totals} />
      ) : null}
    </div>
  );
}
