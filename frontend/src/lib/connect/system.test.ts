import { describe, expect, it } from "vitest";

import { ComponentStatus } from "@/gen/paladin/iam/v1/health_service_pb";
import { componentSegments, componentStatusLabel } from "./system";

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

describe("componentSegments", () => {
  // Disabled is switched off on purpose: a pause, not a fault.
  it("counts the components by state, each in its tone", () => {
    const c = (status: ComponentStatus) => ({ status }) as never;
    expect(
      componentSegments([
        c(ComponentStatus.HEALTHY),
        c(ComponentStatus.HEALTHY),
        c(ComponentStatus.DISABLED),
        c(ComponentStatus.UNHEALTHY),
      ]),
    ).toEqual(
      expect.arrayContaining([
        { tone: "success", label: "healthy", count: 2 },
        { tone: "suspended", label: "disabled", count: 1 },
        { tone: "danger", label: "unhealthy", count: 1 },
      ]),
    );
  });
});
