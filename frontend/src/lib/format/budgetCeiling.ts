// A budget ceiling as every budget_ceiling oneof on the wire spells it: a
// Money limit, where a zero amount allows no spend, or unlimited in a unit.
// Capabilities, tenant budgets and billing summaries all read and write it
// here, so no view mistakes a limit of 0 for "no limit".
//
// A billing summary's oneof has a fourth arm, budgetUnreadable: the budget
// could not be read, so whether there is a cap is unknown. It is read here
// too, so no view mistakes it for "no budget".

import { create } from "@bufbuild/protobuf";

import type { Money } from "@/gen/google/type/money_pb";
import type { BudgetUnreadable } from "@/gen/paladin/admin/v1/billing_service_pb";
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
 * A ceiling as a read may answer it: a billing summary's also says when the
 * budget could not be read.
 */
export type BudgetCeilingRead =
  BudgetCeiling | { case: "budgetUnreadable"; value: BudgetUnreadable };

/** What a ceiling read says of the budget. */
export const BudgetState = {
  /** A cap, which may be zero. */
  Limited: "limited",
  /** Explicitly no cap. */
  Unlimited: "unlimited",
  /** No budget is configured. */
  None: "none",
  /** The budget could not be read; whether there is a cap is unknown. */
  Unreadable: "unreadable",
} as const;
export type BudgetState = (typeof BudgetState)[keyof typeof BudgetState];

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

/** What `c` says of the budget; nothing read is no budget. */
export function budgetState(c: BudgetCeilingRead | undefined): BudgetState {
  switch (c?.case) {
    case "maxBudget":
      return BudgetState.Limited;
    case "unlimitedBudget":
      return BudgetState.Unlimited;
    case "budgetUnreadable":
      return BudgetState.Unreadable;
    default:
      return BudgetState.None;
  }
}

/** The limit, or undefined when there is none or nothing is known. */
export function budgetLimit(
  c: BudgetCeilingRead | undefined,
): Money | undefined {
  return c?.case === "maxBudget" ? c.value : undefined;
}

/** The unit charges are in, limited or not; undefined when unknown. */
export function budgetUnit(
  c: BudgetCeilingRead | undefined,
): string | undefined {
  switch (c?.case) {
    case "maxBudget":
    case "unlimitedBudget":
      return c.value.currencyCode || undefined;
    default:
      return undefined;
  }
}
