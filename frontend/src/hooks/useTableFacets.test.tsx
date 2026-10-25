import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// The router's view of the URL, which a test moves as a navigation would.
const nav = vi.hoisted(() => ({ search: "" }));
vi.mock("next/navigation", () => ({
  useSearchParams: () => new URLSearchParams(nav.search),
}));

import { useServerFacets, useTableFacets } from "./useTableFacets";
import { groupFacetCounts, type Facet } from "@/lib/table/facets";
import { PAGE_TABLE } from "@/lib/table/urlState";

interface Row {
  id: string;
  state: string;
}
const FACETS: readonly Facet<Row>[] = [
  {
    id: "state",
    label: "State",
    values: (r) => [r.state],
    options: [
      { value: "on", label: "On" },
      { value: "off", label: "Off" },
    ],
    initial: ["on"],
  },
];
const ROWS: Row[] = [
  { id: "a", state: "on" },
  { id: "b", state: "off" },
];

function navigate(search: string) {
  window.history.replaceState(null, "", `/list${search}`);
  nav.search = search;
}

beforeEach(() => navigate(""));

describe("useTableFacets", () => {
  it("opens on the initial choice, counted over every row", () => {
    const { result } = renderHook(() => useTableFacets(ROWS, FACETS));
    expect(result.current.rows.map((r) => r.id)).toEqual(["a"]);
    expect(result.current.active).toBe(1);
    expect(result.current.controls[0]).toMatchObject({
      id: "state",
      label: "State",
      selected: ["on"],
      options: [
        { value: "on", count: 1 },
        { value: "off", count: 1 },
      ],
    });
  });

  it("narrows to what a control chooses, and clears every facet", () => {
    const { result } = renderHook(() => useTableFacets(ROWS, FACETS));
    act(() => result.current.controls[0].onChange(["off"]));
    expect(result.current.rows.map((r) => r.id)).toEqual(["b"]);
    act(() => result.current.clear());
    expect(result.current.rows).toBe(ROWS);
    expect(result.current.active).toBe(0);
  });

  it("starts from the URL and writes each change back", () => {
    navigate("?state=off&tab=x");
    const { result } = renderHook(() =>
      useTableFacets(ROWS, FACETS, PAGE_TABLE),
    );
    expect(result.current.rows.map((r) => r.id)).toEqual(["b"]);
    act(() => result.current.controls[0].onChange(["on"]));
    expect(window.location.search).toBe("?tab=x");
    act(() => result.current.clear());
    expect(window.location.search).toBe("?tab=x&state=any");
  });
});

describe("useServerFacets", () => {
  const counts = groupFacetCounts([
    { facet: "state", value: "off", count: 7n },
  ]);

  it("counts each answer as the server did, and filters no rows itself", () => {
    const { result } = renderHook(() => useServerFacets(FACETS));
    expect(result.current.selection).toEqual({ state: ["on"] });
    expect(result.current.controls(counts)[0].options).toMatchObject([
      { value: "on", count: 0 },
      { value: "off", count: 7 },
    ]);
    expect(result.current).not.toHaveProperty("rows");
  });

  it("keeps its choices in the URL", () => {
    navigate("?state=off");
    const { result } = renderHook(() => useServerFacets(FACETS, PAGE_TABLE));
    expect(result.current.selection).toEqual({ state: ["off"] });
    act(() => result.current.controls(counts)[0].onChange(["on", "off"]));
    expect(result.current.selection).toEqual({ state: ["on", "off"] });
    act(() => result.current.clear());
    expect(result.current.active).toBe(0);
    expect(window.location.search).toBe("?state=any");
  });
});
