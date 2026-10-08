import { describe, expect, it } from "vitest";

import { ComponentStatus } from "@/gen/paladin/iam/v1/health_service_pb";
import { componentStatusLabel } from "./system";

describe("componentStatusLabel", () => {
  it.each([
    [ComponentStatus.HEALTHY, "HEALTHY"],
    [ComponentStatus.DEGRADED, "DEGRADED"],
    [ComponentStatus.UNHEALTHY, "UNHEALTHY"],
    [ComponentStatus.DISABLED, "DISABLED"],
    [ComponentStatus.UNSPECIFIED, "UNKNOWN"],
  ])("labels %s as %s", (status, label) => {
    expect(componentStatusLabel(status)).toBe(label);
  });
});
