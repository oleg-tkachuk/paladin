"use client";

// The holds on a tenant's budget: what makes up its reserved amount. Each is
// a metered request's cost set aside until it settles, is released, or
// expires; one past its expiry still counts until housekeeping releases it.
import Link from "next/link";
import { useInfiniteQuery } from "@tanstack/react-query";
import { LockClosedIcon } from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { EmptyTableRow } from "@/components/ui/EmptyState";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Timestamp } from "@/components/Timestamp";
import { errorMessage } from "@/hooks/errorContract";
import { formatMoney } from "@/lib/format/money";
import { capabilityHref } from "@/lib/navigation/hrefs";
import { tenantReservationsQuery } from "@/lib/queries/tenantReservations";
import { T } from "@/lib/ui/typography";

import { holdLapsed } from "./reservations";

/** The table's columns, which its empty row spans. */
const COLUMNS = 5;

/** What the badge on a hold past its expiry says. */
export const LAPSED_LABEL = "expired, not released";

export function TenantReservations({
  tenant,
  tenantId,
  now = new Date(),
}: {
  /** The tenant's slug or id, for links into its workspace. */
  tenant: string;
  tenantId: string;
  /** The instant a hold is judged lapsed against; the render's by default. */
  now?: Date;
}) {
  const query = useInfiniteQuery(tenantReservationsQuery(tenantId));
  const holds = query.data?.pages.flatMap((p) => p.reservations) ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle
          level={3}
          className="text-base font-medium"
          icon={LockClosedIcon}
        >
          Open holds
        </CardTitle>
        <p className={T.helper}>
          Costs set aside for metered requests in flight. They count against the
          cap until each settles or is released.
        </p>
      </CardHeader>
      <CardContent>
        {query.isPending ? (
          <div className="space-y-2">
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-full" />
          </div>
        ) : query.isError ? (
          <ListLoadError
            what="holds"
            reason={errorMessage(query.error)}
            onRetry={() => void query.refetch()}
          />
        ) : (
          <div className="flex flex-col gap-2">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Capability</TableHead>
                  <TableHead numeric>Amount</TableHead>
                  <TableHead>Operation</TableHead>
                  <TableHead>Actor</TableHead>
                  <TableHead>Expires</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {holds.length === 0 ? (
                  <EmptyTableRow
                    colSpan={COLUMNS}
                    icon={LockClosedIcon}
                    what="holds"
                    reason="empty"
                    purpose="A metered request holds its cost here until it settles."
                  />
                ) : (
                  holds.map((h) => (
                    <TableRow key={h.id}>
                      <TableCell className="font-mono text-xs">
                        <Link
                          href={capabilityHref(tenant, h.capabilityId)}
                          className="hover:underline"
                        >
                          {h.capabilityId}
                        </Link>
                      </TableCell>
                      <TableCell numeric>{formatMoney(h.amount)}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {h.op}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {h.actor}
                      </TableCell>
                      <TableCell className="text-sm">
                        <Timestamp ts={h.expiresAt} />
                        {holdLapsed(h.expiresAt, now) && (
                          <Badge variant="outline" className="ml-2">
                            {LAPSED_LABEL}
                          </Badge>
                        )}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
            {query.hasNextPage && (
              <Button
                variant="outline"
                size="sm"
                className="self-start"
                onClick={() => void query.fetchNextPage()}
                disabled={query.isFetchingNextPage}
              >
                {query.isFetchingNextPage ? "Loading…" : "Show more"}
              </Button>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
