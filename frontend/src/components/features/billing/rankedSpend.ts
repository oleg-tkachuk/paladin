// What a ranked spend list shows of its entries: the first few, each with its
// share of the period's total, so a long tail never stretches the card.
import { moneyToNanos } from "@/lib/format/money";
import type { TopEntry } from "@/gen/paladin/admin/v1/billing_service_pb";

/** How many entries a list shows before it is opened. */
export const RANKED_VISIBLE = 5;

const FULL_PERCENT = 100;

/** One entry and its share of the total, 0–100. */
export interface RankedRow {
  entry: TopEntry;
  sharePercent: number;
}

/**
 * The rows of entries to show: all of them when open, else the first
 * RANKED_VISIBLE, each with its share of total — the period's whole spend,
 * so the shares of a top-N list need not sum to 100.
 */
export function rankedRows(
  entries: readonly TopEntry[],
  totalNanos: bigint,
  open: boolean,
): RankedRow[] {
  const shown = open ? entries : entries.slice(0, RANKED_VISIBLE);
  return shown.map((entry) => ({
    entry,
    sharePercent:
      totalNanos > 0n
        ? Math.min(
            FULL_PERCENT,
            (Number(moneyToNanos(entry.spent)) / Number(totalNanos)) *
              FULL_PERCENT,
          )
        : 0,
  }));
}
