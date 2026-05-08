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
} from "@/gen/paladin/iam/v1/system_service_pb";

import { ComponentStatus } from "@/gen/paladin/iam/v1/system_service_pb";

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
    default:
      return "UNKNOWN";
  }
}
