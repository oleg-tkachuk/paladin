// What the storage-backends list shows for a backend, and how its state
// filter reaches the API. Pure, so the row and the filter can be tested
// without rendering the page.

import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import { worstStatus, type NodeStatus } from "@/lib/topology/model";
import { StatusTone } from "@/lib/ui/status";

type BackendFlags = Pick<StorageBackend, "enabled" | "readOnly">;
type BackendHealth = Pick<StorageBackend, "healthStatus" | "healthMessage">;

/** The state-filter choices, in the order the picker lists them. */
export const STATE_FILTERS = [
  "all",
  "active",
  "draining",
  "maintenance",
  "disabled",
] as const;
export type StateFilter = (typeof STATE_FILTERS)[number];

export const STATE_FILTER_LABELS: Record<StateFilter, string> = {
  all: "All states",
  active: "Active",
  draining: "Draining",
  maintenance: "Maintenance",
  disabled: "Disabled",
};

// Each is a conjunction over fields StorageBackendSchema declares, so it joins
// the search conjunct with `&&` and the whole filter stays one chain the
// server can push down (src/lib/cel.ts says why a disjunction must not).
const STATE_FILTER_CEL: Record<StateFilter, string> = {
  all: "",
  active: "enabled == true && read_only == false",
  draining: "enabled == true && read_only == true",
  maintenance: "maintenance == true",
  disabled: "enabled == false",
};

/** The CEL for one state choice; "" for no narrowing. */
export function stateFilter(choice: StateFilter): string {
  return STATE_FILTER_CEL[choice];
}

/** Joins non-empty conjuncts with `&&`; "" when there are none. */
export function allOf(...conjuncts: string[]): string {
  return conjuncts.filter((c) => c !== "").join(" && ");
}

/**
 * The one lifecycle state a row leads with. Disabled outranks draining: a
 * disabled backend refuses everything, so whether it would also refuse writes
 * says nothing more. Disabled is a pause an operator chose, not a fault, and
 * draining is a change under way, not a warning.
 */
export function lifecycleState(b: BackendFlags): {
  label: string;
  tone: StatusTone;
} {
  if (!b.enabled) return { label: "Disabled", tone: StatusTone.Suspended };
  if (b.readOnly) return { label: "Draining", tone: StatusTone.Progress };
  return { label: "Enabled", tone: StatusTone.Success };
}

/** The values healthStatus carries for a probed backend. */
export const HEALTH_OK = "ok";
export const HEALTH_ERROR = "error";

/** What the last connectivity probe found, and the text a hover explains it with. */
export function healthView(b: BackendHealth): {
  label: string;
  tone: StatusTone;
  title: string;
} {
  switch (b.healthStatus) {
    case HEALTH_OK:
      return {
        label: "Healthy",
        tone: StatusTone.Success,
        title: "The last probe succeeded",
      };
    case HEALTH_ERROR:
      return {
        label: "Probe failed",
        tone: StatusTone.Danger,
        title: b.healthMessage || "The last probe failed",
      };
    default:
      return {
        label: "Untested",
        tone: StatusTone.Unknown,
        title: "Not probed yet: run Test connectivity on the backend",
      };
  }
}

/**
 * The one state a backend is drawn in where a single glyph stands for it, as
 * a node of the topology: whichever of its lifecycle, its maintenance flag
 * and its last probe most needs a look. A failed probe on an enabled backend
 * outranks the enablement; an enabled, untested one reads as untested.
 */
export function backendStatus(
  b: BackendFlags & BackendHealth & Pick<StorageBackend, "maintenance">,
): NodeStatus {
  const { label, tone } = healthView(b);
  // The probe first: of equals the first wins, and a working backend reads
  // better as healthy than as merely enabled.
  return worstStatus([
    { label, tone },
    lifecycleState(b),
    b.maintenance
      ? { label: "Maintenance", tone: StatusTone.Suspended }
      : undefined,
  ])!;
}
