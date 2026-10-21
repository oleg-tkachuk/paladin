"use client";

// How many quotas sit at or near their limits, platform-wide, from the
// reconciled census (GetPlatformStats.rls.quotas). The stats page breaks it
// down; this says whether to look.
import { alertTone } from "@/components/ui/IconChip";
import Link from "next/link";
import { ScaleIcon } from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatusBar } from "@/components/ui/StatusBar";
import { StatusTone } from "@/lib/ui/status";
import { SECTION } from "@/lib/navigation/sections";
import { cn } from "@/lib/utils";

import { useQuiet, type QuietCard } from "./quiet";
import { usePlatformSummary } from "./usePlatformSummary";

const QUOTAS_CARD: QuietCard = {
  id: "quotas",
  label: "No quota near its limit",
  href: SECTION.stats,
};

export function QuotaAlertsWidget() {
  const { data, isPending, error } = usePlatformSummary();
  const quotas = data?.rls?.available ? data.rls.quotas : undefined;
  const atLimit = Number(quotas?.atLimit ?? 0);
  const near = Number(quotas?.nearLimit ?? 0);
  // The census counts a row once, at its fullest cap; the rest of the rows
  // with a cap are within their limits.
  const within = Math.max(0, Number(quotas?.withLimits ?? 0) - atLimit - near);
  const alarm = atLimit > 0;
  const quiet = !isPending && !error && !!quotas && atLimit === 0 && near === 0;
  if (useQuiet(QUOTAS_CARD, quiet)) return null;
  return (
    <Card className={cn(alarm && "border-destructive/40")}>
      <CardHeader className="pb-2">
        <CardTitle
          className="text-sm"
          icon={ScaleIcon}
          tone={alertTone(atLimit, near)}
        >
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
        ) : (
          <div className="space-y-2 py-2">
            {atLimit === 0 && near === 0 ? (
              <p className="text-center text-xs text-muted-foreground">
                No quota is near its limit.
              </p>
            ) : (
              <div className="flex flex-wrap items-center gap-2 text-xs">
                {atLimit > 0 && (
                  <Badge variant="destructive">{atLimit} at their limit</Badge>
                )}
                {near > 0 && <Badge variant="warning">{near} near it</Badge>}
              </div>
            )}
            <StatusBar
              segments={[
                { tone: StatusTone.Danger, label: "at limit", count: atLimit },
                { tone: StatusTone.Warning, label: "near", count: near },
                { tone: StatusTone.Success, label: "within", count: within },
              ]}
            />
          </div>
        )}
      </CardContent>
    </Card>
  );
}
