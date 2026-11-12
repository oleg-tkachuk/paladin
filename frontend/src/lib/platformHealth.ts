import { timestampDate } from "@bufbuild/protobuf/wkt";

import type {
  GetPlatformHealthResponse,
  RoleHealth,
} from "@/gen/paladin/admin/v1/system_service_pb";
import {
  ComponentControl,
  ComponentStatus as PbStatus,
  type ComponentHealth,
} from "@/gen/paladin/iam/v1/health_service_pb";

import type {
  Component,
  ComponentStatus,
  Control,
  HealthAll,
  RoleStatus,
  Snapshot,
} from "./health";

// The admin role's GetPlatformHealth, in the shape the health page renders
// (ADR-0048: the console reads it through the admin plane, holding no
// health token of its own).

const STATUS: Record<PbStatus, ComponentStatus> = {
  // Never read an unknown status as healthy: a probe bug must show.
  [PbStatus.UNSPECIFIED]: "unhealthy",
  [PbStatus.HEALTHY]: "healthy",
  [PbStatus.DEGRADED]: "degraded",
  [PbStatus.UNHEALTHY]: "unhealthy",
  [PbStatus.DISABLED]: "disabled",
};

const CONTROL: Record<ComponentControl, Control | undefined> = {
  [ComponentControl.UNSPECIFIED]: undefined,
  [ComponentControl.ALWAYS_ON]: "always_on",
  [ComponentControl.CONFIG]: "config",
  [ComponentControl.DATABASE]: "database",
};

function componentStatus(s: PbStatus): ComponentStatus {
  return STATUS[s] ?? "unhealthy";
}

/** A role is never "disabled"; one reported so reads as unhealthy. */
function roleStatus(s: PbStatus): RoleStatus {
  const status = componentStatus(s);
  return status === "disabled" ? "unhealthy" : status;
}

function component(c: ComponentHealth): Component {
  return {
    name: c.name,
    status: componentStatus(c.status),
    message: c.message || undefined,
    latency_ms: Number(c.latencyMs),
    category: c.category,
    critical: c.critical,
    control: CONTROL[c.control],
    details: c.details.length
      ? c.details.map((d) => ({
          name: d.name,
          value: d.value,
          ...(d.group ? { group: d.group } : {}),
        }))
      : undefined,
  };
}

export function snapshotFromRoleHealth(r: RoleHealth): Snapshot {
  return {
    role: r.health?.role ?? "",
    status: roleStatus(r.health?.status ?? PbStatus.UNSPECIFIED),
    components: (r.health?.components ?? []).map(component),
    checked_at: r.checkedAt ? timestampDate(r.checkedAt).toISOString() : "",
  };
}

export function healthAllFromResponse(
  res: GetPlatformHealthResponse,
): HealthAll {
  return { roles: res.roles.map(snapshotFromRoleHealth) };
}
