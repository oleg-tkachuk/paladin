// A capability's request ceiling, as the API spells it: a limit, where 0
// allows nothing, or unlimited. Its budget ceiling is every budget's
// (@/lib/format/budgetCeiling).

import type { CapabilityCaveats } from "@/gen/paladin/admin/v1/capability_service_pb";

/** The request ceiling to issue with: unlimited, or a limit of `limit`. */
export function requestCeiling(
  unlimited: boolean,
  limit: bigint,
): CapabilityCaveats["requestCeiling"] {
  return unlimited
    ? { case: "unlimitedRequests", value: true }
    : { case: "maxRequests", value: limit };
}

/** The request limit, or null when there is none. */
export function requestLimit(c: CapabilityCaveats | undefined): bigint | null {
  return c?.requestCeiling.case === "maxRequests"
    ? c.requestCeiling.value
    : null;
}
