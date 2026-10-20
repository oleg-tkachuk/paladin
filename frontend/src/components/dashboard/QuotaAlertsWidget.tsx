"use client";

// How many quotas sit at or near their limits, platform-wide, from the
// reconciled census (GetPlatformStats.rls.quotas). The stats page breaks it
// down; this says whether to look.
import { IconChip, alertTone } from "@/components/ui/IconChip";
import Link from "next/link";
import { ScaleIcon } from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { SECTION } from "@/lib/navigation/sections";
import { cn } from "@/lib/utils";

import { usePlatformSummary } from "./usePlatformSummary";

export function QuotaAlertsWidget() {
  const { data, isPending, error } = usePlatformSummary();
  const quotas = data?.rls?.available ? data.rls.quotas : undefined;
  const atLimit = Number(quotas?.atLimit ?? 0);
  const near = Number(quotas?.nearLimit ?? 0);
  const alarm = atLimit > 0;
  return (
    <Card className={cn(alarm && "border-destructive/40")}>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <IconChip icon={ScaleIcon} tone={alertTone(atLimit, near)} />
          Quotas
          <Link
            href={SECTION.stats}
            className="ml-auto text-xs font-normal text-muted-foreground hover:text-foreground"
          >
            Platform stats
          </Link>
        </CardTitle>
      </CardHeader>
      <CardContent className="pt-0">
        {isPending ? (
          <Skeleton className="h-12 w-full" />
        ) : error || !quotas ? (
          <p className="py-4 text-center text-xs text-muted-foreground">
            The quota census is unavailable.
          </p>
        ) : atLimit === 0 && near === 0 ? (
          <p className="py-4 text-center text-xs text-muted-foreground">
            No quota is near its limit.
          </p>
        ) : (
          <div className="flex flex-wrap items-center gap-2 py-2 text-xs">
            {atLimit > 0 && (
              <Badge variant="destructive">{atLimit} at their limit</Badge>
            )}
            {near > 0 && <Badge variant="warning">{near} near it</Badge>}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
