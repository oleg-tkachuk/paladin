import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { describe, expect, it } from "vitest";

import {
  GetPlatformHealthResponseSchema,
  RoleHealthSchema,
} from "@/gen/paladin/admin/v1/system_service_pb";
import {
  ComponentControl,
  ComponentStatus,
  HealthInfoSchema,
} from "@/gen/paladin/iam/v1/health_service_pb";

import {
  healthAllFromResponse,
  snapshotFromRoleHealth,
} from "./platformHealth";

const AT_MS = Date.UTC(2026, 10, 12, 10, 0, 0);

function role(
  name: string,
  status: ComponentStatus,
  components = [] as object[],
) {
  return create(RoleHealthSchema, {
    health: create(HealthInfoSchema, {
      role: name,
      status,
      components: components as never,
    }),
    checkedAt: timestampFromMs(AT_MS),
  });
}

describe("snapshotFromRoleHealth", () => {
  it("keeps the role, its status, its checks and when they ran", () => {
    const s = snapshotFromRoleHealth(
      role("iam", ComponentStatus.DEGRADED, [
        {
          name: "postgres",
          status: ComponentStatus.HEALTHY,
          latencyMs: 4n,
          category: "database",
          critical: true,
          control: ComponentControl.ALWAYS_ON,
          details: [
            { name: "applied", value: "56", group: "schema" },
            { name: "pool", value: "8", group: "" },
          ],
        },
        {
          name: "nats",
          status: ComponentStatus.DISABLED,
          message: "not in use",
          control: ComponentControl.DATABASE,
        },
      ]),
    );
    expect(s.role).toBe("iam");
    expect(s.status).toBe("degraded");
    expect(s.checked_at).toBe(new Date(AT_MS).toISOString());
    expect(s.components[0]).toEqual({
      name: "postgres",
      status: "healthy",
      message: undefined,
      latency_ms: 4,
      category: "database",
      critical: true,
      control: "always_on",
      details: [
        { name: "applied", value: "56", group: "schema" },
        { name: "pool", value: "8" },
      ],
    });
    expect(s.components[1]).toMatchObject({
      status: "disabled",
      message: "not in use",
      control: "database",
      details: undefined,
    });
  });

  it("never reads an unknown status as healthy", () => {
    const s = snapshotFromRoleHealth(
      role("api", ComponentStatus.UNSPECIFIED, [
        { name: "x", status: ComponentStatus.UNSPECIFIED },
      ]),
    );
    expect(s.status).toBe("unhealthy");
    expect(s.components[0].status).toBe("unhealthy");
  });

  it("reads a role reported disabled as unhealthy", () => {
    expect(
      snapshotFromRoleHealth(role("mcp", ComponentStatus.DISABLED)).status,
    ).toBe("unhealthy");
  });
});

describe("healthAllFromResponse", () => {
  it("keeps every role in the order the admin role sent", () => {
    const all = healthAllFromResponse(
      create(GetPlatformHealthResponseSchema, {
        roles: [
          role("worker", ComponentStatus.HEALTHY),
          role("api", ComponentStatus.UNHEALTHY),
        ],
      }),
    );
    expect(all.roles.map((r) => [r.role, r.status])).toEqual([
      ["worker", "healthy"],
      ["api", "unhealthy"],
    ]);
  });
});
