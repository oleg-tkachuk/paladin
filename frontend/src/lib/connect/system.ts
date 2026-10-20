// Re-exports + small helpers around paladin.iam.v1.SystemService.
//
// Replaces the earlier system-stub.ts that throw'd on every call while the
// backend RPC didn't exist. Keeping the import path "@/lib/connect/system"
// stable means consumers (StatsContext, useStats, /health page, etc.)
// only need their type imports adjusted.

export {
  ComponentStatus,
  type ComponentHealth,
  type HealthInfo,
  type VersionInfo,
} from "@/gen/paladin/iam/v1/health_service_pb";

import {
  ComponentStatus,
  type ComponentHealth,
} from "@/gen/paladin/iam/v1/health_service_pb";
import type { StatusSegment } from "@/components/ui/StatusBar";
import { StatusTone } from "@/lib/ui/status";

/**
 * componentStatusLabel maps the proto enum to the upper-case string the
 * existing UI code (color pills, sort keys, etc.) compares against. Keep
 * this stable — log scrapers and saved views may match the literal.
 */
export function componentStatusLabel(s: ComponentStatus): string {
  switch (s) {
    case ComponentStatus.HEALTHY:
      return "HEALTHY";
    case ComponentStatus.DEGRADED:
      return "DEGRADED";
    case ComponentStatus.UNHEALTHY:
      return "UNHEALTHY";
    case ComponentStatus.DISABLED:
      return "DISABLED";
    default:
      return "UNKNOWN";
  }
}

// Each state's tone and its name in a tally. Disabled is switched off on
// purpose, so it is a pause, not a fault.
const COMPONENT_SEGMENT: Record<
  ComponentStatus,
  { tone: StatusTone; label: string }
> = {
  [ComponentStatus.HEALTHY]: { tone: StatusTone.Success, label: "healthy" },
  [ComponentStatus.DEGRADED]: { tone: StatusTone.Warning, label: "degraded" },
  [ComponentStatus.UNHEALTHY]: {
    tone: StatusTone.Danger,
    label: "unhealthy",
  },
  [ComponentStatus.DISABLED]: {
    tone: StatusTone.Suspended,
    label: "disabled",
  },
  [ComponentStatus.UNSPECIFIED]: { tone: StatusTone.Unknown, label: "unknown" },
};

/** How the components split by state, for a StatusBar. */
export function componentSegments(
  components: readonly ComponentHealth[],
): StatusSegment[] {
  const counts = new Map<ComponentStatus, number>();
  for (const c of components) {
    const s =
      c.status in COMPONENT_SEGMENT ? c.status : ComponentStatus.UNSPECIFIED;
    counts.set(s, (counts.get(s) ?? 0) + 1);
  }
  return [...counts].map(([s, count]) => ({ ...COMPONENT_SEGMENT[s], count }));
}
