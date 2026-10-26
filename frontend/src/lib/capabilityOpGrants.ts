// What a capability's ops unlock with no tenant policy, read against the
// table CapabilityService.ListOperationGrants serves (ADR-0038). Each row
// says on what basis it unlocks; the console reads that basis and infers
// nothing from what a row leaves empty.
import { CapabilityGrantBasis } from "@/gen/paladin/admin/v1/capability_service_pb";

/**
 * One row of the server's table, as CapabilityOperationGrant carries it: an
 * op, the basis of what it unlocks, and the field that basis fills.
 */
export interface OperationGrant {
  op: string;
  basis: CapabilityGrantBasis;
  /** The Cedar actions, for CapabilityGrantBasis.BUILTIN. */
  actions: readonly string[];
  /** The ops this one is asserted beside, for CapabilityGrantBasis.PAIRED. */
  pairedOps: readonly string[];
  /** The RPCs, as package.Service.Method, for CAVEATS_ONLY. */
  procedures: readonly string[];
}

/** An op the built-in policy grants, with the actions it grants. */
export interface BuiltinGrant {
  op: string;
  actions: readonly string[];
}

/** An op asserted only beside others, which grant the action. */
export interface PairedGrant {
  op: string;
  pairedOps: readonly string[];
}

/** An op the caveats alone decide, with the RPCs it opens. */
export interface CaveatsOnlyGrant {
  op: string;
  procedures: readonly string[];
}

/** What a set of ops unlocks, each op in the order it was given. */
export interface CapabilityOpGrants {
  /** Ops the built-in policy grants actions under. */
  granted: BuiltinGrant[];
  /** Ops that unlock nothing until a tenant policy names their actions. */
  needsPolicy: string[];
  /** Ops that add a requirement beside other ops and unlock nothing alone. */
  paired: PairedGrant[];
  /** Ops whose caveats alone decide, with no policy asked. */
  caveatsOnly: CaveatsOnlyGrant[];
  /**
   * Ops the table cannot answer for: no row, a basis this console does not
   * know (UNSPECIFIED included), or a row missing the field its basis
   * fills. Never counted as granted.
   */
  unknown: string[];
}

/**
 * Sorts the selected ops by the basis of what they unlock. Each op is
 * counted once. A row whose basis names a field it leaves empty — BUILTIN
 * with no actions, PAIRED with no ops, CAVEATS_ONLY with no procedures — is
 * a server inconsistency, not a grant, and falls to unknown.
 */
export function capabilityOpGrants(
  selectedOps: Iterable<string>,
  grants: readonly OperationGrant[],
): CapabilityOpGrants {
  const byOp = new Map(grants.map((g) => [g.op, g]));
  const out: CapabilityOpGrants = {
    granted: [],
    needsPolicy: [],
    paired: [],
    caveatsOnly: [],
    unknown: [],
  };
  for (const op of new Set(selectedOps)) {
    const grant = byOp.get(op);
    switch (grant?.basis) {
      case CapabilityGrantBasis.BUILTIN:
        if (grant.actions.length > 0) {
          out.granted.push({ op, actions: grant.actions });
          continue;
        }
        break;
      case CapabilityGrantBasis.TENANT_POLICY:
        out.needsPolicy.push(op);
        continue;
      case CapabilityGrantBasis.PAIRED:
        if (grant.pairedOps.length > 0) {
          out.paired.push({ op, pairedOps: grant.pairedOps });
          continue;
        }
        break;
      case CapabilityGrantBasis.CAVEATS_ONLY:
        if (grant.procedures.length > 0) {
          out.caveatsOnly.push({ op, procedures: grant.procedures });
          continue;
        }
        break;
    }
    out.unknown.push(op);
  }
  return out;
}

/** Separates a procedure's package, service and method. */
const PROCEDURE_SEPARATOR = ".";
/** Service.Method: the segments a procedure is shown by, from the end. */
const SERVICE_METHOD_SEGMENTS = 2;

/**
 * A procedure as the console names it: Service.Method, without the package
 * every procedure shares. A name with fewer segments is shown whole.
 */
export function procedureLabel(procedure: string): string {
  return procedure
    .split(PROCEDURE_SEPARATOR)
    .slice(-SERVICE_METHOD_SEGMENTS)
    .join(PROCEDURE_SEPARATOR);
}
