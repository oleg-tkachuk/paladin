import { describe, expect, it } from "vitest";

import { filterByFacets, initialSelection } from "./facets";
import { LIFECYCLE_BADGE, lifecycleFacet, type Lifecycle } from "./lifecycle";

const STATES = Object.keys(LIFECYCLE_BADGE) as Lifecycle[];
const FACET = lifecycleFacet((row: { state: Lifecycle }) => row.state);

describe("lifecycleFacet", () => {
  it("offers every state", () => {
    expect(FACET.options?.map((o) => o.value).sort()).toEqual(
      [...STATES].sort(),
    );
  });

  it("shows the live rows first", () => {
    const rows = STATES.map((state) => ({ state }));
    const shown = filterByFacets(rows, [FACET], initialSelection([FACET]));
    expect(shown).toEqual([{ state: "active" }]);
  });
});
