// What a capability's ops unlock with no tenant policy, read against the
// table CapabilityService.ListOperationGrants serves (ADR-0038).

/** One row of the server's table: an op and the Cedar actions it unlocks. */
export interface OperationGrant {
  op: string;
  actions: readonly string[];
}

/** What a set of ops unlocks, each op in the order it was given. */
export interface CapabilityOpGrants {
  /** Ops that unlock actions on their own, with those actions. */
  granted: OperationGrant[];
  /** Ops the table names but unlocks nothing for: a tenant policy must. */
  needsPolicy: string[];
  /** Ops the table does not name at all. */
  unknown: string[];
}

/**
 * An op in the table with no actions unlocks nothing without a tenant
 * policy: the server sends an empty list for it, never a sentinel action.
 */
export function needsTenantPolicy(grant: OperationGrant): boolean {
  return grant.actions.length === 0;
}

/**
 * Sorts the selected ops into what they unlock, what needs a tenant policy,
 * and what the table does not know. Each op is counted once.
 */
export function capabilityOpGrants(
  selectedOps: Iterable<string>,
  grants: readonly OperationGrant[],
): CapabilityOpGrants {
  const byOp = new Map(grants.map((g) => [g.op, g]));
  const out: CapabilityOpGrants = { granted: [], needsPolicy: [], unknown: [] };
  for (const op of new Set(selectedOps)) {
    const grant = byOp.get(op);
    if (grant === undefined) out.unknown.push(op);
    else if (needsTenantPolicy(grant)) out.needsPolicy.push(op);
    else out.granted.push({ op, actions: grant.actions });
  }
  return out;
}
