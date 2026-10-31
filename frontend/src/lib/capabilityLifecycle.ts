import {
  CapabilityDisposition,
  type Capability,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import { lifecycleFacet, type Lifecycle } from "@/lib/table/lifecycle";

/**
 * A capability's disposition (CapabilityService.List) in the lifecycle every
 * credential-like table names, colours and filters alike, and back: the
 * facet's choices become the dispositions the server narrows to.
 */

// UNSPECIFIED is a server too old to say, which listed only the active ones.
const LIFECYCLE_OF = {
  [CapabilityDisposition.UNSPECIFIED]: "active",
  [CapabilityDisposition.ACTIVE]: "active",
  [CapabilityDisposition.REVOKED]: "revoked",
  [CapabilityDisposition.EXPIRED]: "expired",
} as const satisfies Record<CapabilityDisposition, Lifecycle>;

const DISPOSITION_OF = {
  active: CapabilityDisposition.ACTIVE,
  revoked: CapabilityDisposition.REVOKED,
  expired: CapabilityDisposition.EXPIRED,
} as const satisfies Record<Lifecycle, CapabilityDisposition>;

/** A capability's lifecycle. */
export function capabilityLifecycle(c: Capability): Lifecycle {
  return LIFECYCLE_OF[c.disposition];
}

/** The state facet, narrowing the listing on the server. */
export const CAPABILITY_FACETS = [lifecycleFacet(capabilityLifecycle)];

/**
 * The dispositions to ask the server for, from the facet's choices. None
 * chosen is every disposition: the server reads none as the active ones.
 */
export function dispositionsFor(
  chosen: readonly string[],
): CapabilityDisposition[] {
  const known = chosen.filter((v): v is Lifecycle => v in DISPOSITION_OF);
  const lifecycles =
    known.length > 0 ? known : (Object.keys(DISPOSITION_OF) as Lifecycle[]);
  return lifecycles.map((l) => DISPOSITION_OF[l]);
}
