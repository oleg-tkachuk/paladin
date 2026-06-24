import { describe, expect, it } from "vitest";

import { nextSort, type SortState } from "./useTableSort";

type Col = "name" | "size";
const unsorted: SortState<Col> = { column: null, direction: null };

describe("nextSort", () => {
  it("starts a fresh column at asc", () => {
    expect(nextSort(unsorted, "name")).toEqual({
      column: "name",
      direction: "asc",
    });
  });

  it("cycles the same column asc → desc → unsorted → asc", () => {
    let s = nextSort(unsorted, "name"); // asc
    s = nextSort(s, "name");
    expect(s).toEqual({ column: "name", direction: "desc" });
    s = nextSort(s, "name");
    expect(s).toEqual({ column: null, direction: null }); // cleared
    s = nextSort(s, "name");
    expect(s).toEqual({ column: "name", direction: "asc" }); // wraps
  });

  it("switching columns restarts at asc regardless of prior direction", () => {
    const descName: SortState<Col> = { column: "name", direction: "desc" };
    expect(nextSort(descName, "size")).toEqual({
      column: "size",
      direction: "asc",
    });
  });

  it("is pure — does not mutate the previous state", () => {
    const prev: SortState<Col> = { column: "name", direction: "asc" };
    const frozen = Object.freeze({ ...prev });
    expect(() => nextSort(frozen, "name")).not.toThrow();
    expect(prev).toEqual({ column: "name", direction: "asc" });
  });
});
