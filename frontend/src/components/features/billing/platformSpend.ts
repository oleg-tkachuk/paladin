// The platform's spend over a period, read off the tenants that spent: what
// they came to together, how many charges, and each tenant's share. Pure, so
// the headline figures and the table's shares agree.
import type { TenantSpend } from "@/gen/paladin/admin/v1/billing_service_pb";
import type { Money } from "@/gen/google/type/money_pb";
import {
  moneyFromNanos,
  moneyPercent,
  moneyToNanos,
  unitOf,
} from "@/lib/format/money";

export interface PlatformTotals {
  /**
   * What the tenants in the leading unit spent together. The server lists
   * the platform's currency first, and amounts in other units do not add to
   * it, so the total is that unit's.
   */
  total: Money;
  /** Every listed tenant's charges, whatever their unit. */
  charges: number;
  /** What a charge in the total's unit came to, on average. */
  perCharge: Money;
  /** The tenants that spent. */
  tenants: number;
  /** Those that spent in another unit than the total's, left out of it. */
  otherUnit: number;
}

export function platformTotals(
  tenants: readonly TenantSpend[],
  fallbackUnit: string,
): PlatformTotals {
  const unit = tenants[0] ? unitOf(tenants[0].spent) : fallbackUnit;
  const inUnit = tenants.filter((t) => unitOf(t.spent) === unit);
  const totalNanos = inUnit.reduce((acc, t) => acc + moneyToNanos(t.spent), 0n);
  const unitCharges = inUnit.reduce((acc, t) => acc + t.chargeCount, 0n);
  return {
    total: moneyFromNanos(totalNanos, unit),
    charges: tenants.reduce((acc, t) => acc + Number(t.chargeCount), 0),
    perCharge: moneyFromNanos(
      unitCharges > 0n ? totalNanos / unitCharges : 0n,
      unit,
    ),
    tenants: tenants.length,
    otherUnit: tenants.length - inUnit.length,
  };
}

/** A tenant's share of the total, in percent; null in another unit. */
export function spendShare(
  t: TenantSpend,
  totals: PlatformTotals,
): number | null {
  if (unitOf(t.spent) !== unitOf(totals.total)) return null;
  return moneyPercent(t.spent, totals.total);
}
