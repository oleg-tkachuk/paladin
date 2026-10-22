"use client";

import { useCallback } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { ChartBarIcon } from "@heroicons/react/24/outline";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { errorMessage } from "@/hooks/errorContract";
import type { TimeBucket } from "@/gen/paladin/admin/v1/billing_service_pb";
import { formatDate } from "@/lib/format/locale";
import { formatMoney, moneyFromNanos } from "@/lib/format/money";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { GRANULARITIES } from "./period";
import { Segmented } from "./Segmented";
import { SpendChart } from "./SpendChart";
import type { Granularity } from "./spendSeries";
import { pointSpent, type SpendStats } from "./spendStats";

// Spend is shown to four decimals so a sub-cent charge does not read as
// nothing.
const SPEND_DIGITS = 4;
// The chart takes numbers of whole units.
const NANOS_PER_UNIT = 1e9;

/**
 * How spend moved over the period: a bar a bucket, its peak and average,
 * and the granularity to draw it at. A tenant's billing and the platform's
 * both show one.
 */
export function SpendOverTimeCard({
  buckets,
  stats,
  unit,
  granularity,
  allowed,
  onGranularity,
  emptyText,
  error,
  onRetry,
}: {
  /** The series' buckets; undefined while it loads. */
  buckets: readonly TimeBucket[] | undefined;
  stats: SpendStats;
  /** The unit the axis and figures are in. */
  unit: string;
  granularity: Granularity;
  /** The granularities the period allows; the rest are offered disabled. */
  allowed: readonly Granularity[];
  onGranularity: (g: Granularity) => void;
  /** What the card says of a period with no spend. */
  emptyText: string;
  /** Why the series could not be read; the card says so rather than wait. */
  error: unknown;
  onRetry: () => void;
}) {
  const formatAxis = useCallback(
    (v: number) =>
      formatMoney(moneyFromNanos(BigInt(Math.round(v * NANOS_PER_UNIT)), unit)),
    [unit],
  );
  const money = (m: Parameters<typeof formatMoney>[0]) =>
    formatMoney(m, { fallbackUnit: unit, fractionDigits: SPEND_DIGITS });
  const { peak, avgNanos, points } = stats;
  return (
    <Card>
      <CardHeader className="flex flex-wrap items-center justify-between gap-3">
        <CardTitle level={3} className={T.cardTitleProse} icon={ChartBarIcon}>
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
          onChange={onGranularity}
        />
      </CardHeader>
      <CardContent className="space-y-3">
        {error && !buckets ? (
          <ListLoadError
            what="Spend over time"
            reason={errorMessage(error, "Failed to load the spend over time")}
            onRetry={onRetry}
          />
        ) : !buckets ? (
          <Skeleton className="h-48" />
        ) : buckets.length === 0 ? (
          <div className={cn(T.helper, "py-8 text-center")}>{emptyText}</div>
        ) : (
          <>
            <SpendChart
              points={points}
              granularity={granularity}
              formatValue={formatAxis}
              formatPoint={(p) => money(pointSpent(p))}
            />
            <div className={cn(T.hint, "flex flex-wrap gap-x-6 gap-y-1")}>
              {peak && peak.start ? (
                <span>
                  Peak: {money(peak.spent)} on{" "}
                  {formatDate(timestampDate(peak.start))}
                </span>
              ) : null}
              <span>
                Average: {money(moneyFromNanos(avgNanos, unit))} per{" "}
                {granularity}
              </span>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}
