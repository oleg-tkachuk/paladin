import { describe, expect, it } from "vitest";

import {
  HEALTH_ERROR,
  HEALTH_OK,
  STATE_FILTERS,
  allOf,
  healthView,
  lifecycleState,
  stateFilter,
} from "./backendView";

describe("stateFilter", () => {
  it.each([
    ["all", ""],
    ["active", "enabled == true && read_only == false"],
    ["draining", "enabled == true && read_only == true"],
    ["maintenance", "maintenance == true"],
    ["disabled", "enabled == false"],
  ] as const)("%s → %s", (choice, cel) => {
    expect(stateFilter(choice)).toBe(cel);
  });

  // A disjunction pushes nothing down (src/lib/cel.ts); every choice must
  // stay a conjunction so it can sit in the same && chain as the search.
  it("never builds a disjunction", () => {
    for (const choice of STATE_FILTERS) {
      expect(stateFilter(choice)).not.toContain("||");
    }
  });
});

describe("allOf", () => {
  it("joins the non-empty conjuncts", () => {
    expect(allOf('search.contains("eu")', "enabled == false")).toBe(
      'search.contains("eu") && enabled == false',
    );
  });

  it("drops empty ones, and is empty when all are", () => {
    expect(allOf("", "maintenance == true", "")).toBe("maintenance == true");
    expect(allOf("", "")).toBe("");
  });
});

describe("lifecycleState", () => {
  it.each([
    [{ enabled: true, readOnly: false }, "Enabled", "success"],
    [{ enabled: true, readOnly: true }, "Draining", "warning"],
    [{ enabled: false, readOnly: false }, "Disabled", "destructive"],
    // A disabled backend refuses everything; draining adds nothing.
    [{ enabled: false, readOnly: true }, "Disabled", "destructive"],
  ])("%o → %s", (flags, label, variant) => {
    expect(lifecycleState(flags)).toEqual({ label, variant });
  });
});

describe("healthView", () => {
  it("is healthy after a successful probe", () => {
    expect(healthView({ healthStatus: HEALTH_OK, healthMessage: "" })).toEqual(
      expect.objectContaining({ label: "Healthy", tone: "ok" }),
    );
  });

  it("carries the probe's error as its explanation", () => {
    expect(
      healthView({ healthStatus: HEALTH_ERROR, healthMessage: "refused" }),
    ).toEqual({ label: "Probe failed", tone: "error", title: "refused" });
  });

  it("falls back to a generic explanation when the probe left none", () => {
    expect(
      healthView({ healthStatus: HEALTH_ERROR, healthMessage: "" }).title,
    ).toBe("The last probe failed");
  });

  it("is untested for anything else", () => {
    for (const status of ["", "unknown"]) {
      expect(healthView({ healthStatus: status, healthMessage: "" })).toEqual(
        expect.objectContaining({ label: "Untested", tone: "unknown" }),
      );
    }
  });
});
