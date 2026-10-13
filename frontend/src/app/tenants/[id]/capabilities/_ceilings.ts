// A capability's request and budget ceilings, as the API spells them: each a
// limit, where 0 allows nothing, or unlimited. Read and written here only, so
// no view mistakes a limit of 0 for "no limit" again.

import { create } from "@bufbuild/protobuf";

import type { Money } from "@/gen/google/type/money_pb";
import {
  CapabilityUnlimitedBudgetSchema,
  type CapabilityCaveats,
} from "@/gen/paladin/admin/v1/capability_service_pb";

/** The request ceiling to issue with: unlimited, or a limit of `limit`. */
export function requestCeiling(
  unlimited: boolean,
  limit: bigint,
): CapabilityCaveats["requestCeiling"] {
  return unlimited
    ? { case: "unlimitedRequests", value: true }
    : { case: "maxRequests", value: limit };
}

/**
 * The budget ceiling to issue with: unlimited in `unit`, or a limit of
 * `limit`, which carries its own unit.
 */
export function budgetCeiling(
  unlimited: boolean,
  limit: Money,
  unit: string,
): CapabilityCaveats["budgetCeiling"] {
  return unlimited
    ? {
        case: "unlimitedBudget",
        value: create(CapabilityUnlimitedBudgetSchema, { currencyCode: unit }),
      }
    : { case: "maxBudget", value: limit };
}

/** The request limit, or null when there is none. */
export function requestLimit(c: CapabilityCaveats | undefined): bigint | null {
  return c?.requestCeiling.case === "maxRequests"
    ? c.requestCeiling.value
    : null;
}

/** The budget limit, or undefined when there is none. */
export function budgetLimit(
  c: CapabilityCaveats | undefined,
): Money | undefined {
  return c?.budgetCeiling.case === "maxBudget"
    ? c.budgetCeiling.value
    : undefined;
}

/** The unit charges are in, limited or not; undefined when unknown. */
export function budgetUnit(
  c: CapabilityCaveats | undefined,
): string | undefined {
  switch (c?.budgetCeiling.case) {
    case "maxBudget":
      return c.budgetCeiling.value.currencyCode || undefined;
    case "unlimitedBudget":
      return c.budgetCeiling.value.currencyCode || undefined;
    default:
      return undefined;
  }
}
