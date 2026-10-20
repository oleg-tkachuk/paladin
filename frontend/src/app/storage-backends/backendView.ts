// What the storage-backends list shows for a backend, and how its state
// filter reaches the API. Pure, so the row and the filter can be tested
// without rendering the page.

import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
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
 * The one state a backend leads with in the list's status bar and its tile:
 * its lifecycle and its last probe, folded so each backend counts once.
 * Disabled first, because an operator chose it and it refuses everything;
 * then a failed probe, which needs a look; then a maintenance flag and a
 * drain, changes under way; then an untested backend; then a healthy one.
 */
export function backendStatus(
  b: BackendFlags & BackendHealth & Pick<StorageBackend, "maintenance">,
): { label: string; tone: StatusTone } {
  if (!b.enabled) return lifecycleState(b);
  const { label, tone } = healthView(b);
  if (b.healthStatus === HEALTH_ERROR) return { label, tone };
  if (b.maintenance) {
    return { label: "Maintenance", tone: StatusTone.Suspended };
  }
  if (b.readOnly) return lifecycleState(b);
  return { label, tone };
}
