// A capability's request ceiling and its quota of Biscuit copies, as the API
// spells them: a limit, where 0 allows nothing, or unlimited. Its budget
// ceiling is every budget's (@/lib/format/budgetCeiling).

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

/** A whole number an operator typed, or null when it is not one. */
export function wholeNumber(text: string): bigint | null {
  const trimmed = text.trim();
  return /^\d+$/.test(trimmed) ? BigInt(trimmed) : null;
}

/** The quota of Biscuit copies to issue with: unlimited, or `limit`. */
export function copyCeiling(
  unlimited: boolean,
  limit: bigint,
): CapabilityCaveats["copyCeiling"] {
  return unlimited
    ? { case: "unlimitedCopies", value: true }
    : { case: "maxCopies", value: limit };
}

/**
 * The quota of Biscuit copies, or null when there is none: unlimited, which
 * the server still bounds per capability, or caveats that set neither.
 */
export function copyLimit(c: CapabilityCaveats | undefined): bigint | null {
  return c?.copyCeiling.case === "maxCopies" ? c.copyCeiling.value : null;
}
