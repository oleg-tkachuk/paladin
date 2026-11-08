// What billing says of the spend returned from a period's charges: a call
// that failed after it was charged is refunded, and the spend shown is net of
// it. One wording for every place an amount is shown beside its refunds.
import { formatMoney, moneyToNanos } from "@/lib/format/money";
import type { Money } from "@/gen/google/type/money_pb";

/**
 * The note for what was refunded, "$0.2500 refunded", or undefined when
 * nothing was: an absent amount and a zero one say the same.
 */
export function refundedNote(
  refunded: Money | undefined,
  opts: { fallbackUnit?: string; fractionDigits?: number } = {},
): string | undefined {
  if (moneyToNanos(refunded) <= BigInt(0)) return undefined;
  return `${formatMoney(refunded, opts)} refunded`;
}
