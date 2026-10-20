import { describe, expect, it } from "vitest";

import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import { countOptions, filterByFacets } from "@/lib/table/facets";

import {
  BACKEND_FACETS,
  HEALTH_ERROR,
  HEALTH_OK,
  healthView,
  lifecycleState,
} from "./backendView";

const backend = (over: Partial<StorageBackend>) =>
  ({
    enabled: true,
    readOnly: false,
    maintenance: false,
    healthStatus: "",
    healthMessage: "",
    ...over,
  }) as StorageBackend;

const [STATE, HEALTH] = BACKEND_FACETS;

describe("BACKEND_FACETS", () => {
  const rows = [
    backend({ healthStatus: HEALTH_OK }),
    backend({ readOnly: true, maintenance: true, healthStatus: HEALTH_ERROR }),
    backend({ enabled: false }),
  ];

  it("counts a backend in maintenance under its state as well", () => {
    const counts = Object.fromEntries(
      countOptions(rows, STATE, []).map((o) => [o.value, o.count]),
    );
    expect(counts).toEqual({
      enabled: 1,
      draining: 1,
      disabled: 1,
      maintenance: 1,
    });
  });

  it("names every health a probe can leave", () => {
    expect(
      countOptions(rows, HEALTH, []).map((o) => [o.label, o.count]),
    ).toEqual([
      ["Healthy", 1],
      ["Probe failed", 1],
      ["Untested", 1],
    ]);
  });

  it("keeps a backend giving any chosen state, and passing every facet", () => {
    expect(
      filterByFacets(rows, BACKEND_FACETS, {
        state: ["disabled", "maintenance"],
      }),
    ).toEqual([rows[1], rows[2]]);
    expect(
      filterByFacets(rows, BACKEND_FACETS, {
        state: ["disabled", "maintenance"],
        health: [HEALTH_ERROR],
      }),
    ).toEqual([rows[1]]);
  });
});

describe("lifecycleState", () => {
  it.each([
    [{ enabled: true, readOnly: false }, "Enabled", "success"],
    [{ enabled: true, readOnly: true }, "Draining", "progress"],
    [{ enabled: false, readOnly: false }, "Disabled", "suspended"],
    // A disabled backend refuses everything; draining adds nothing.
    [{ enabled: false, readOnly: true }, "Disabled", "suspended"],
  ])("%o → %s", (flags, label, tone) => {
    expect(lifecycleState(flags)).toEqual({ label, tone });
  });
});

describe("healthView", () => {
  it("is healthy after a successful probe", () => {
    expect(healthView({ healthStatus: HEALTH_OK, healthMessage: "" })).toEqual(
      expect.objectContaining({ label: "Healthy", tone: "success" }),
    );
  });

  it("carries the probe's error as its explanation", () => {
    expect(
      healthView({ healthStatus: HEALTH_ERROR, healthMessage: "refused" }),
    ).toEqual({ label: "Probe failed", tone: "danger", title: "refused" });
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
