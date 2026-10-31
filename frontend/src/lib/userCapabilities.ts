import {
  UserCapability_State,
  type UserCapability,
} from "@/gen/paladin/iam/v1/user_service_pb";
import { SECTION } from "@/lib/navigation/sections";
import { lifecycleFacet, type Lifecycle } from "@/lib/table/lifecycle";
import type { TableUrl } from "@/lib/table/urlState";

/**
 * The capabilities acting on a user's behalf (UserService.ListUserCapabilities):
 * what an agent exchanged under one of the user's OAuth grants. One contract for
 * the user's own page and an operator's view of a user.
 */

/**
 * The table of a user's capabilities, namespaced so it sits beside the user
 * page's grants table.
 */
export const USER_CAPABILITIES_TABLE: TableUrl = { namespace: "caps" };

/** The page parameter narrowing the list to one grant: its id. */
export const USER_CAPABILITIES_GRANT_PARAM = "grant";

/** The signed-in user's capabilities, or one grant's of them. */
export function myCapabilitiesHref(grant?: string): string {
  if (!grant) return SECTION.myCapabilities;
  const params = new URLSearchParams({
    [USER_CAPABILITIES_GRANT_PARAM]: grant,
  });
  return `${SECTION.myCapabilities}?${params}`;
}

// UNSPECIFIED is a server too old to say, which lists only the active ones.
const LIFECYCLE_OF = {
  [UserCapability_State.UNSPECIFIED]: "active",
  [UserCapability_State.ACTIVE]: "active",
  [UserCapability_State.REVOKED]: "revoked",
  [UserCapability_State.EXPIRED]: "expired",
} as const satisfies Record<UserCapability_State, Lifecycle>;

const STATE_OF = {
  active: UserCapability_State.ACTIVE,
  revoked: UserCapability_State.REVOKED,
  expired: UserCapability_State.EXPIRED,
} as const satisfies Record<Lifecycle, UserCapability_State>;

/** A capability's lifecycle, as every credential-like table names it. */
export function userCapabilityLifecycle(c: UserCapability): Lifecycle {
  return LIFECYCLE_OF[c.state];
}

/** The state facet, narrowing the listing on the server. */
export const USER_CAPABILITY_FACETS = [lifecycleFacet(userCapabilityLifecycle)];

/**
 * The states to ask the server for, from the facet's choices. None chosen is
 * every state: the server reads an empty list as the active ones alone.
 */
export function statesFor(chosen: readonly string[]): UserCapability_State[] {
  const known = chosen.filter((v): v is Lifecycle => v in STATE_OF);
  const lifecycles =
    known.length > 0 ? known : (Object.keys(STATE_OF) as Lifecycle[]);
  return lifecycles.map((l) => STATE_OF[l]);
}
