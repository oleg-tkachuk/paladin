import { describe, expect, it } from "vitest";

import {
  activeFacets,
  countedOptions,
  countOptions,
  facetUpdates,
  filterByFacets,
  groupFacetCounts,
  initialSelection,
  parseFacets,
  type Facet,
} from "./facets";
import { PAGE_TABLE, type TableUrl } from "./urlState";

interface Row {
  id: string;
  state: string;
  tags: string[];
}
const STATE: Facet<Row> = {
  id: "state",
  label: "State",
  values: (r) => [r.state],
  options: [
    { value: "on", label: "On" },
    { value: "off", label: "Off" },
  ],
  initial: ["on"],
};
// Free-form: the menu offers what the rows carry.
const TAG: Facet<Row> = { id: "tag", label: "Tag", values: (r) => r.tags };
const FACETS = [STATE, TAG];
const GRANTS: TableUrl = { namespace: "grants" };

const rows: Row[] = [
  { id: "a", state: "on", tags: ["eu", "prod"] },
  { id: "b", state: "off", tags: ["eu"] },
  { id: "c", state: "on", tags: [] },
];
const ids = (rs: readonly Row[]) => rs.map((r) => r.id);

describe("filterByFacets", () => {
  it("keeps every row, as given, when no facet narrows", () => {
    expect(filterByFacets(rows, FACETS, { state: [], tag: [] })).toBe(rows);
    expect(filterByFacets(rows, FACETS, {})).toBe(rows);
  });

  it("keeps a row giving any answer chosen within a facet", () => {
    expect(ids(filterByFacets(rows, FACETS, { state: ["on", "off"] }))).toEqual(
      ["a", "b", "c"],
    );
    expect(ids(filterByFacets(rows, FACETS, { tag: ["prod", "eu"] }))).toEqual([
      "a",
      "b",
    ]);
  });

  it("keeps only a row passing every facet", () => {
    expect(
      ids(filterByFacets(rows, FACETS, { state: ["on"], tag: ["eu"] })),
    ).toEqual(["a"]);
  });
});

describe("countOptions", () => {
  it("counts each answer of a fixed vocabulary, in its order", () => {
    expect(countOptions(rows, STATE, [])).toEqual([
      { value: "on", label: "On", count: 2 },
      { value: "off", label: "Off", count: 1 },
    ]);
  });

  it("offers the answers the rows give when there is no vocabulary", () => {
    expect(countOptions(rows, TAG, []).map((o) => [o.value, o.count])).toEqual([
      ["eu", 2],
      ["prod", 1],
    ]);
  });

  it("counts a row giving the same answer twice once", () => {
    const twice = [{ id: "x", state: "on", tags: ["eu", "eu"] }];
    expect(countOptions(twice, TAG, [])).toEqual([
      { value: "eu", label: "eu", count: 1 },
    ]);
  });

  it("still offers a chosen answer no row gives, so it can be unchosen", () => {
    expect(countOptions(rows, TAG, ["gone"]).at(-1)).toEqual({
      value: "gone",
      label: "gone",
      count: 0,
    });
  });
});

describe("activeFacets", () => {
  it("counts the facets with a choice", () => {
    expect(activeFacets({ state: ["on"], tag: [] })).toBe(1);
    expect(activeFacets(initialSelection([TAG]))).toBe(0);
  });
});

describe("the URL form", () => {
  const read = (search: string, url = PAGE_TABLE) =>
    parseFacets(new URLSearchParams(search), FACETS, url);

  it("reads an absent parameter as the initial choice", () => {
    expect(read("")).toEqual({ state: ["on"], tag: [] });
    expect(read("")).toEqual(initialSelection(FACETS));
  });

  it("reads several answers, dropping repeats and unknown fixed ones", () => {
    expect(read("?state=off,bogus,off&tag=eu,prod")).toEqual({
      state: ["off"],
      tag: ["eu", "prod"],
    });
  });

  it("reads a facet that opens narrowed, cleared, as no choice", () => {
    expect(read("?state=any").state).toEqual([]);
  });

  it("keeps an embedded table's facets under its namespace", () => {
    expect(read("?grants.tag=eu&tag=prod", GRANTS).tag).toEqual(["eu"]);
    expect(facetUpdates({ state: ["off"], tag: [] }, FACETS, GRANTS)).toEqual({
      "grants.state": "off",
      "grants.tag": null,
    });
  });

  it("leaves the initial choice out, and says when one was cleared", () => {
    expect(
      facetUpdates({ state: ["on"], tag: [] }, FACETS, PAGE_TABLE),
    ).toEqual({ state: null, tag: null });
    expect(facetUpdates({ state: [], tag: [] }, FACETS, PAGE_TABLE)).toEqual({
      state: "any",
      tag: null,
    });
  });

  it("reads back what it writes", () => {
    const selection = { state: ["off", "on"], tag: ["eu", "prod"] };
    const updates = facetUpdates(selection, FACETS, PAGE_TABLE);
    const params = new URLSearchParams(
      Object.entries(updates).filter(
        (e): e is [string, string] => e[1] !== null,
      ),
    );
    expect(parseFacets(params, FACETS, PAGE_TABLE)).toEqual({
      state: ["on", "off"],
      tag: ["eu", "prod"],
    });
  });
});

describe("counts a server gave", () => {
  const counts = groupFacetCounts([
    { facet: "state", value: "on", count: 4n },
    { facet: "tag", value: "eu", count: 2n },
    { facet: "tag", value: "prod", count: 1 },
  ]);

  it("offers a fixed vocabulary in its order, an uncounted answer at nought", () => {
    expect(countedOptions(STATE, counts.get("state") ?? new Map(), [])).toEqual(
      [
        { value: "on", label: "On", count: 4 },
        { value: "off", label: "Off", count: 0 },
      ],
    );
  });

  it("offers the answers counted, named by the facet, and a chosen one at nought", () => {
    const named = { ...TAG, optionLabel: (v: string) => v.toUpperCase() };
    expect(
      countedOptions(named, counts.get("tag") ?? new Map(), ["us"]),
    ).toEqual([
      { value: "eu", label: "EU", count: 2 },
      { value: "prod", label: "PROD", count: 1 },
      { value: "us", label: "US", count: 0 },
    ]);
  });

  it("reads each answer through the value given", () => {
    const renamed = groupFacetCounts(
      [{ facet: "state", value: "STATE_ON", count: 3n }],
      (c) => c.value.replace("STATE_", "").toLowerCase(),
    );
    expect(renamed.get("state")?.get("on")).toBe(3);
  });
});
