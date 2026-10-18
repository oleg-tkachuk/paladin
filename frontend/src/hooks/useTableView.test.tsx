import { describe, expect, it } from "vitest";
import { act, renderHook } from "@testing-library/react";

import { useTableView } from "./useTableView";

interface Row {
  name: string;
  size: number;
}
const rows: Row[] = [
  { name: "beta", size: 2 },
  { name: "alpha", size: 3 },
  { name: "alphabet", size: 1 },
];
const options = {
  text: (r: Row) => [r.name],
  accessors: { name: (r: Row) => r.name, size: (r: Row) => r.size },
};

describe("useTableView", () => {
  it("shows the rows as given until searched or sorted", () => {
    const { result } = renderHook(() => useTableView(rows, options));
    expect(result.current.rows).toBe(rows);
    expect(result.current.total).toBe(rows.length);
  });

  it("searches, then sorts what the search kept, keeping the total", () => {
    const { result } = renderHook(() => useTableView(rows, options));
    act(() => result.current.setQuery("alpha"));
    act(() => result.current.toggleSort("size"));
    expect(result.current.rows.map((r) => r.name)).toEqual([
      "alphabet",
      "alpha",
    ]);
    expect(result.current.total).toBe(rows.length);
    act(() => result.current.toggleSort("size"));
    expect(result.current.rows.map((r) => r.name)).toEqual([
      "alpha",
      "alphabet",
    ]);
  });

  it("starts from the sort it is given", () => {
    const { result } = renderHook(() =>
      useTableView(rows, {
        ...options,
        initialSort: { column: "name", direction: "asc" },
      }),
    );
    expect(result.current.rows.map((r) => r.name)).toEqual([
      "alpha",
      "alphabet",
      "beta",
    ]);
  });
});
