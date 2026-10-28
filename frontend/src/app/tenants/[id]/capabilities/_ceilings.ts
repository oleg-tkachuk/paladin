// A capability's request ceiling and its quota of Biscuit copies, as the API
// spells them: a limit or unlimited. A request limit is at least
// MIN_MAX_REQUESTS; a copy quota of 0 allows no copy with limits of its own. Its budget
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

/**
 * The least request limit the server issues a capability with: one no
 * request can use is refused (max_requests in capability_service.proto).
 */
export const MIN_MAX_REQUESTS = BigInt(1);

/** A request limit an operator typed that can be issued, or null. */
export function issuableRequestLimit(text: string): bigint | null {
  const limit = wholeNumber(text);
  return limit !== null && limit >= MIN_MAX_REQUESTS ? limit : null;
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
