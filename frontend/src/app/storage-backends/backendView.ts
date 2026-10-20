// What the storage-backends list shows for a backend, and the facets it
// filters by. Pure, so the row and the filters can be tested without
// rendering the page.

import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import type { Facet } from "@/lib/table/facets";
import { worstStatus, type NodeStatus } from "@/lib/topology/model";
import { StatusTone } from "@/lib/ui/status";

type BackendFlags = Pick<StorageBackend, "enabled" | "readOnly">;
type BackendHealth = Pick<StorageBackend, "healthStatus" | "healthMessage">;

/**
 * The lifecycle states a backend can lead with. Disabled is a pause an
 * operator chose, not a fault, and draining is a change under way, not a
 * warning.
 */
const LIFECYCLE = {
  enabled: { label: "Enabled", tone: StatusTone.Success },
  draining: { label: "Draining", tone: StatusTone.Progress },
  disabled: { label: "Disabled", tone: StatusTone.Suspended },
} as const satisfies Record<string, { label: string; tone: StatusTone }>;
type LifecycleKey = keyof typeof LIFECYCLE;

/**
 * Which state a backend leads with. Disabled outranks draining: a disabled
 * backend refuses everything, so whether it would also refuse writes says
 * nothing more.
 */
function lifecycleKey(b: BackendFlags): LifecycleKey {
  if (!b.enabled) return "disabled";
  return b.readOnly ? "draining" : "enabled";
}

/** The one lifecycle state a row leads with, and its tone. */
export function lifecycleState(b: BackendFlags): {
  label: string;
  tone: StatusTone;
} {
  const { label, tone } = LIFECYCLE[lifecycleKey(b)];
  return { label, tone };
}

/** The values healthStatus carries for a probed backend. */
export const HEALTH_OK = "ok";
export const HEALTH_ERROR = "error";

/** The health facet's answer for a backend no probe has reached. */
const HEALTH_UNTESTED = "untested";

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

/** The facet answer for maintenance, which a backend carries beside its state. */
const MAINTENANCE = "maintenance";

/**
 * The facets the list filters by. Maintenance is advisory and orthogonal to
 * the lifecycle, so it is one more answer to State that a backend gives as
 * well as its own: choosing Enabled and Maintenance lists either.
 */
export const BACKEND_FACETS: readonly Facet<StorageBackend>[] = [
  {
    id: "state",
    label: "State",
    values: (b) =>
      b.maintenance ? [lifecycleKey(b), MAINTENANCE] : [lifecycleKey(b)],
    options: [
      ...Object.entries(LIFECYCLE).map(([value, s]) => ({ value, ...s })),
      { value: MAINTENANCE, label: "Maintenance", tone: StatusTone.Suspended },
    ],
  },
  {
    id: "health",
    label: "Health",
    values: (b) => [
      b.healthStatus === HEALTH_OK || b.healthStatus === HEALTH_ERROR
        ? b.healthStatus
        : HEALTH_UNTESTED,
    ],
    options: [HEALTH_OK, HEALTH_ERROR, HEALTH_UNTESTED].map((value) => {
      const { label, tone } = healthView({
        healthStatus: value,
        healthMessage: "",
      });
      return { value, label, tone };
    }),
  },
];

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
