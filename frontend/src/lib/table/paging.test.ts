import { describe, expect, it } from "vitest";

import {
  DEFAULT_PAGE_SIZE,
  clampPage,
  pageCount,
  pageOf,
  pageSlice,
  parsePage,
  readPageSize,
} from "./paging";

const rows = Array.from({ length: 23 }, (_, i) => i + 1);

describe("readPageSize", () => {
  it("reads a size the picker offers, and anything else as the default", () => {
    expect(readPageSize(25)).toBe(25);
    expect(readPageSize("all")).toBe("all");
    expect(readPageSize(7)).toBe(DEFAULT_PAGE_SIZE);
    expect(readPageSize(undefined)).toBe(DEFAULT_PAGE_SIZE);
  });
});

describe("pageCount", () => {
  it.each([
    [23, 10, 3],
    [20, 10, 2],
    [0, 10, 1],
    [500, "all", 1],
  ] as const)("%i rows of %s a page take %i", (total, size, want) => {
    expect(pageCount(total, size)).toBe(want);
  });
});

describe("clampPage", () => {
  it.each([
    [0, 3, 1],
    [-2, 3, 1],
    [Number.NaN, 3, 1],
    [2, 3, 2],
    [9, 3, 3],
  ])("page %d of %d is %d", (page, count, want) => {
    expect(clampPage(page, count)).toBe(want);
  });
});

describe("pageOf", () => {
  it("finds the page a row falls on", () => {
    expect(pageOf(0, 10)).toBe(1);
    expect(pageOf(9, 10)).toBe(1);
    expect(pageOf(10, 10)).toBe(2);
    expect(pageOf(400, "all")).toBe(1);
  });
});

describe("pageSlice", () => {
  it("cuts a page and says where it sits", () => {
    expect(pageSlice(rows, 1, 10)).toEqual({
      rows: rows.slice(0, 10),
      first: 1,
      last: 10,
    });
    expect(pageSlice(rows, 3, 10)).toEqual({
      rows: [21, 22, 23],
      first: 21,
      last: 23,
    });
  });

  it("shows every row, as given, at all", () => {
    expect(pageSlice(rows, 1, "all").rows).toBe(rows);
  });

  it("is the range nought to nought for no rows", () => {
    expect(pageSlice([], 1, 10)).toEqual({ rows: [], first: 0, last: 0 });
  });
});

describe("parsePage", () => {
  it.each([
    [null, 1],
    ["", 1],
    ["3", 3],
    ["0", 1],
    ["-1", 1],
    ["2.5", 1],
    ["two", 1],
  ])("%s is page %i", (raw, want) => {
    expect(parsePage(raw)).toBe(want);
  });
});
