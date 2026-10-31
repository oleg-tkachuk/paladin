import {
  PrincipalKind,
  type Capability,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import { tenantHref } from "@/lib/navigation/hrefs";

/**
 * The agent run a capability belongs to (subject.run_id): what
 * CapabilityService.RevokeRun revokes and List narrows to by run_id.
 */

/** The capabilities page's parameter naming the run it lists. */
export const CAPABILITY_RUN_PARAM = "run";

/** The run of an agent's capability; null for one that names none. */
export function capabilityRun(cap: Capability): string | null {
  const s = cap.subject;
  if (s?.kind !== PrincipalKind.AGENT || !s.runId) return null;
  return s.runId;
}

/** A run's capabilities, on its tenant's capabilities page. */
export function runCapabilitiesHref(tenant: string, run: string): string {
  const params = new URLSearchParams({ [CAPABILITY_RUN_PARAM]: run });
  return `${tenantHref(tenant)}/capabilities?${params}`;
}
