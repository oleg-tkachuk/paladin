// A budget ceiling as every budget_ceiling oneof on the wire spells it: a
// Money limit, where a zero amount allows no spend, or unlimited in a unit.
// Capabilities, tenant budgets and billing summaries all read and write it
// here, so no view mistakes a limit of 0 for "no limit".

import { create } from "@bufbuild/protobuf";

import type { Money } from "@/gen/google/type/money_pb";
import {
  UnlimitedBudgetSchema,
  type UnlimitedBudget,
} from "@/gen/paladin/admin/v1/types_pb";

/** The shape every generated budget_ceiling oneof has. */
export type BudgetCeiling =
  | { case: "maxBudget"; value: Money }
  | { case: "unlimitedBudget"; value: UnlimitedBudget }
  | { case: undefined; value?: undefined };

/**
 * The ceiling to send: unlimited in `unit`, or a limit of `limit`, which
 * carries its own unit.
 */
export function budgetCeiling(
  unlimited: boolean,
  limit: Money,
  unit: string,
): BudgetCeiling {
  return unlimited
    ? {
        case: "unlimitedBudget",
        value: create(UnlimitedBudgetSchema, { currencyCode: unit }),
      }
    : { case: "maxBudget", value: limit };
}

/** The limit, or undefined when there is none or nothing is known. */
export function budgetLimit(c: BudgetCeiling | undefined): Money | undefined {
  return c?.case === "maxBudget" ? c.value : undefined;
}

/** Whether the ceiling is explicitly unlimited. */
export function budgetUnlimited(c: BudgetCeiling | undefined): boolean {
  return c?.case === "unlimitedBudget";
}

/** The unit charges are in, limited or not; undefined when unknown. */
export function budgetUnit(c: BudgetCeiling | undefined): string | undefined {
  return c?.case === undefined ? undefined : c.value.currencyCode || undefined;
}
