import { describe, expect, it } from "vitest";

import { filterRows, sortRows, type SortAccessors } from "./view";

interface Row {
  name: string;
  size?: number;
  at?: Date;
  tags: string[];
}

const rows: Row[] = [
  { name: "beta", size: 20, at: new Date(2026, 1, 1), tags: ["read"] },
  { name: "Alpha", size: 3, at: new Date(2026, 0, 1), tags: ["read", "write"] },
  { name: "gamma", tags: [] },
  { name: "item10", size: 3, tags: [] },
  { name: "item9", size: 20, tags: [] },
];

const text = (r: Row) => [r.name, r.tags.join(" ")];

const accessors: SortAccessors<Row, "name" | "size" | "at"> = {
  name: (r) => r.name,
  size: (r) => r.size,
  at: (r) => r.at,
};

const names = (rs: readonly Row[]) => rs.map((r) => r.name);

describe("filterRows", () => {
  it("keeps every row, and the same array, for an empty query", () => {
    expect(filterRows(rows, "   ", text)).toBe(rows);
  });

  it("ignores case on both sides", () => {
    expect(names(filterRows(rows, "ALPHA", text))).toEqual(["Alpha"]);
  });

  it("needs every word, found in any field", () => {
    expect(names(filterRows(rows, "write alp", text))).toEqual(["Alpha"]);
    expect(names(filterRows(rows, "write beta", text))).toEqual([]);
  });

  it("matches inside a value", () => {
    expect(names(filterRows(rows, "tem", text))).toEqual(["item10", "item9"]);
  });

  it("skips absent fields", () => {
    expect(
      filterRows(rows, "undefined", (r) => [r.at ? "x" : undefined]),
    ).toEqual([]);
  });
});

describe("sortRows", () => {
  it("returns the input unchanged when unsorted", () => {
    expect(sortRows(rows, { column: null, direction: null }, accessors)).toBe(
      rows,
    );
    expect(sortRows(rows, { column: "name", direction: null }, accessors)).toBe(
      rows,
    );
  });

  it("orders text by locale, ignoring case, numbers within it numerically", () => {
    expect(
      names(sortRows(rows, { column: "name", direction: "asc" }, accessors)),
    ).toEqual(["Alpha", "beta", "gamma", "item9", "item10"]);
  });

  it("orders numbers and dates by value, descending when asked", () => {
    expect(
      names(sortRows(rows, { column: "at", direction: "desc" }, accessors)),
    ).toEqual(["beta", "Alpha", "gamma", "item10", "item9"]);
  });

  it("keeps ties in their original order and absent values last, either way", () => {
    expect(
      names(sortRows(rows, { column: "size", direction: "asc" }, accessors)),
    ).toEqual(["Alpha", "item10", "beta", "item9", "gamma"]);
    expect(
      names(sortRows(rows, { column: "size", direction: "desc" }, accessors)),
    ).toEqual(["beta", "item9", "Alpha", "item10", "gamma"]);
  });

  it("does not reorder the input", () => {
    const before = names(rows);
    sortRows(rows, { column: "name", direction: "desc" }, accessors);
    expect(names(rows)).toEqual(before);
  });
});
