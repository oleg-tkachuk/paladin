import { describe, expect, it } from "vitest";

import {
  DEFAULT_LIFECYCLE_FILTER,
  LIFECYCLE_BADGE,
  LIFECYCLE_FILTERS,
  keepsLifecycle,
  type Lifecycle,
} from "./lifecycle";

const STATES = Object.keys(LIFECYCLE_BADGE) as Lifecycle[];

describe("lifecycle", () => {
  it("offers a filter for every state, and all of them", () => {
    expect(LIFECYCLE_FILTERS.map((f) => f.value).sort()).toEqual(
      [...STATES, "all"].sort(),
    );
  });

  it("shows the live rows first", () => {
    expect(DEFAULT_LIFECYCLE_FILTER).toBe("active");
  });

  it.each(STATES)("keeps %s rows under its own filter and all", (state) => {
    expect(keepsLifecycle(state, state)).toBe(true);
    expect(keepsLifecycle("all", state)).toBe(true);
    for (const other of STATES.filter((s) => s !== state)) {
      expect(keepsLifecycle(other, state)).toBe(false);
    }
  });
});
