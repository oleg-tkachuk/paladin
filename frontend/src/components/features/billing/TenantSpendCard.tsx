"use client";

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

import { tenantLabel } from "./tenantLabel";

// Spend is shown to four decimals so a sub-cent charge does not read as
// nothing, as the rest of billing shows it.
const SPEND_DIGITS = 4;
// The skeleton rows while the list loads.
const SKELETON_ROWS = 3;

/**
 * Every tenant that spent over the period, in the order the server ranks
 * them — the platform's currency first, then the most spent — each leading
 * to that tenant's billing.
 */
export function TenantSpendCard({
  period,
  onPick,
}: {
  period: BillingPeriod;
  onPick: (tenantId: string) => void;
}) {
  const query = useQuery(tenantSpendQuery(period));
  const tenants = query.data?.tenants;
  return (
    <Card>
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
                  <TableHead className="text-right">Spend</TableHead>
                  <TableHead className="text-right">Charges</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tenants.map((t) => (
                  <TableRow key={t.tenantId}>
                    <TableCell className="max-w-64 truncate">
                      <button
                        type="button"
                        className="text-left text-primary hover:underline"
                        onClick={() => onPick(t.tenantId)}
                      >
                        {tenantLabel(t)}
                      </button>
                    </TableCell>
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
