"use client";

import Link from "next/link";
import { useQuery } from "@tanstack/react-query";
import { BuildingOffice2Icon } from "@heroicons/react/24/outline";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Count } from "@/components/ui/Count";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableHead,
  TableRow,
} from "@/components/ui/table";
import { errorMessage } from "@/hooks/errorContract";
import { formatMoney } from "@/lib/format/money";
import { tenantSpendQuery, type BillingPeriod } from "@/lib/queries/billing";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { spendShare, type PlatformTotals } from "./platformSpend";
import { tenantLabel } from "./tenantLabel";

// Spend is shown to four decimals so a sub-cent charge does not read as
// nothing, as the rest of billing shows it.
const SPEND_DIGITS = 4;
// The skeleton rows while the list loads.
const SKELETON_ROWS = 3;

/**
 * Every tenant that spent over the period, in the order the server ranks
 * them — the platform's currency first, then the most spent — each a link
 * to that tenant's billing.
 */
export function TenantSpendCard({
  period,
  hrefOf,
  totals,
}: {
  period: BillingPeriod;
  /** Where a tenant's row leads, by its slug, or its id when it has none. */
  hrefOf: (tenant: string) => string;
  /** The platform's totals, for each tenant's share; none without them. */
  totals?: PlatformTotals | null;
}) {
  const query = useQuery(tenantSpendQuery(period));
  const tenants = query.data?.tenants;
  return (
    <Card className="@container">
      <CardHeader>
        <CardTitle
          level={3}
          className={T.cardTitleProse}
          icon={BuildingOffice2Icon}
        >
          Tenants by spend
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {query.error ? (
          <ListLoadError
            what="Tenants by spend"
            reason={errorMessage(query.error, "Failed to load billing")}
            onRetry={() => void query.refetch()}
          />
        ) : !tenants ? (
          <div className="space-y-2">
            {Array.from({ length: SKELETON_ROWS }, (_, i) => (
              <Skeleton key={i} className="h-8" />
            ))}
          </div>
        ) : tenants.length === 0 ? (
          <div className={T.helper}>
            No tenant spent anything in this period.
          </div>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Tenant</TableHead>
                  {totals ? (
                    <TableHead className="hidden w-48 @md:table-cell">
                      Share
                    </TableHead>
                  ) : null}
                  <TableHead className="text-right">Spend</TableHead>
                  <TableHead className="text-right">Charges</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tenants.map((t) => (
                  <TableRow key={t.tenantId}>
                    <TableCell className="w-full max-w-0">
                      <Link
                        href={hrefOf(t.slug || t.tenantId)}
                        className="block truncate text-primary hover:underline"
                        title={tenantLabel(t)}
                      >
                        {tenantLabel(t)}
                      </Link>
                      {/* The slug the tenant's pages are addressed by, when
                          the name is not it already. */}
                      {t.slug && t.displayName && t.slug !== t.displayName ? (
                        <span
                          className={cn(
                            T.codeSmall,
                            "block truncate text-muted-foreground",
                          )}
                        >
                          {t.slug}
                        </span>
                      ) : null}
                    </TableCell>
                    {totals ? (
                      <TableCell className="hidden @md:table-cell">
                        <ShareBar percent={spendShare(t, totals)} />
                      </TableCell>
                    ) : null}
                    <TableCell numeric>
                      {formatMoney(t.spent, { fractionDigits: SPEND_DIGITS })}
                    </TableCell>
                    <TableCell numeric className="font-mono text-sm">
                      <Count value={t.chargeCount} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {query.data?.truncated ? (
              <p className={T.helper}>
                More tenants spent than are listed; the ones that spent the most
                are shown.
              </p>
            ) : null}
          </>
        )}
      </CardContent>
    </Card>
  );
}

// A share is written to one decimal, so a small tenant does not read as 0%.
const SHARE_DIGITS = 1;
// The whole of the platform's spend, the bar's end.
const FULL_PERCENT = 100;

/**
 * A tenant's share of the platform's spend, as a bar and its percent; a dash
 * for one in another unit, which the total does not add.
 */
function ShareBar({ percent }: { percent: number | null }) {
  if (percent === null) return <span className={T.hint}>—</span>;
  return (
    <div className="flex items-center gap-2">
      <div
        role="progressbar"
        aria-label="Share of the platform's spend"
        aria-valuemin={0}
        aria-valuemax={FULL_PERCENT}
        aria-valuenow={Math.round(percent)}
        className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted"
      >
        <div
          className="h-full rounded-full bg-primary"
          style={{ width: `${percent}%` }}
        />
      </div>
      <span className={cn(T.hint, "w-12 text-right tabular-nums")}>
        {percent.toFixed(SHARE_DIGITS)}%
      </span>
    </div>
  );
}
