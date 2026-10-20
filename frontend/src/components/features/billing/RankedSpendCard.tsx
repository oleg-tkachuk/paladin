"use client";

import { useState } from "react";
import { TableCellsIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { Count } from "@/components/ui/Count";
import { IconTone } from "@/components/ui/IconChip";
import { formatMoney } from "@/lib/format/money";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import type { TopEntry } from "@/gen/paladin/admin/v1/billing_service_pb";

import { RANKED_VISIBLE, rankedRows } from "./rankedSpend";

// Spend is shown to four decimals so a sub-cent charge does not read as
// nothing, as the rest of billing shows it.
const SPEND_DIGITS = 4;

/**
 * A breakdown of the period's spend — by capability, actor or operation —
 * ranked as the server ranks it, the first few shown with their share of
 * the period, the rest one click away, so the card keeps its size.
 */
export function RankedSpendCard({
  title,
  entries,
  totalNanos,
  fallbackUnit,
  renderLabel,
  emptyHint,
}: {
  title: string;
  entries: TopEntry[];
  /** The period's whole spend, which each entry's share is of. */
  totalNanos: bigint;
  /** Unit for an entry whose amount carries none. */
  fallbackUnit: string;
  renderLabel: (label: string) => React.ReactNode;
  emptyHint: string;
}) {
  const [open, setOpen] = useState(false);
  const rows = rankedRows(entries, totalNanos, open);
  const hidden = entries.length - RANKED_VISIBLE;
  return (
    <Card className="flex flex-col">
      <CardHeader>
        <CardTitle
          level={3}
          className={T.cardTitleProse}
          icon={TableCellsIcon}
          tone={IconTone.Neutral}
        >
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex-1 space-y-3">
        {entries.length === 0 ? (
          <p className={T.helper}>{emptyHint}</p>
        ) : (
          <ol className="space-y-3">
            {rows.map(({ entry, sharePercent }) => (
              <li key={entry.label} className="space-y-1.5">
                <div className="flex items-baseline justify-between gap-3">
                  <span className={cn(T.body, "min-w-0 truncate")}>
                    {renderLabel(entry.label)}
                  </span>
                  <span className={cn(T.valueInline, "shrink-0")}>
                    {formatMoney(entry.spent, {
                      fallbackUnit,
                      fractionDigits: SPEND_DIGITS,
                    })}
                  </span>
                </div>
                <div
                  className="h-1 w-full rounded bg-muted"
                  role="presentation"
                >
                  <div
                    className="h-full rounded bg-primary/60"
                    style={{ width: `${sharePercent}%` }}
                  />
                </div>
                <p className={T.hint}>
                  <Count value={entry.chargeCount} /> charges ·{" "}
                  {sharePercent.toFixed(1)}% of the period
                </p>
              </li>
            ))}
          </ol>
        )}
        {hidden > 0 ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setOpen((o) => !o)}
            aria-expanded={open}
          >
            {open ? "Show fewer" : `Show all ${entries.length}`}
          </Button>
        ) : null}
      </CardContent>
    </Card>
  );
}
