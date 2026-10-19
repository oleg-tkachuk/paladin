import { describe, expect, it } from "vitest";

import {
  PAGE_TABLE,
  choiceUpdate,
  parseChoice,
  parseSort,
  sortUpdates,
  tableParam,
  withParams,
  type TableUrl,
} from "./urlState";

const GRANTS: TableUrl = { namespace: "grants" };
const COLUMNS = ["name", "created"] as const;
type Column = (typeof COLUMNS)[number];
const NO_SORT = { column: null, direction: null };

describe("tableParam", () => {
  it("names the page's list bare and an embedded one under its namespace", () => {
    expect(tableParam("query", PAGE_TABLE)).toBe("q");
    expect(tableParam("sort", PAGE_TABLE)).toBe("sort");
    expect(tableParam("dir", GRANTS)).toBe("grants.dir");
    expect(tableParam("filter", GRANTS)).toBe("grants.filter");
  });
});

describe("withParams", () => {
  it.each([
    ["sets a parameter", "", { q: "a b" }, "?q=a+b"],
    ["keeps the ones it does not name", "?tab=x", { q: "y" }, "?tab=x&q=y"],
    ["replaces a value", "?q=old", { q: "new" }, "?q=new"],
    ["removes on null", "?q=a&tab=x", { q: null }, "?tab=x"],
    ["removes on empty", "?q=a", { q: "" }, ""],
    ["leaves nothing as no query string", "", { q: null }, ""],
  ])("%s", (_, search, updates, want) => {
    expect(withParams(search, updates)).toBe(want);
  });
});

describe("parseSort", () => {
  const params = (qs: string) => new URLSearchParams(qs);
  it("reads a column the table sorts by", () => {
    expect(
      parseSort<Column>(
        params("sort=created&dir=desc"),
        PAGE_TABLE,
        COLUMNS,
        NO_SORT,
      ),
    ).toEqual({ column: "created", direction: "desc" });
  });
  it.each([
    ["a column the table lacks", "sort=size&dir=asc"],
    ["a direction that is neither", "sort=name&dir=up"],
    ["a column without a direction", "sort=name"],
    ["nothing", ""],
  ])("falls back on %s", (_, qs) => {
    const fallback = { column: "name" as Column, direction: "asc" as const };
    expect(parseSort(params(qs), PAGE_TABLE, COLUMNS, fallback)).toBe(fallback);
  });
  it("reads only its own namespace", () => {
    expect(
      parseSort<Column>(params("sort=name&dir=asc"), GRANTS, COLUMNS, NO_SORT),
    ).toBe(NO_SORT);
    expect(
      parseSort<Column>(
        params("grants.sort=name&grants.dir=asc"),
        GRANTS,
        COLUMNS,
        NO_SORT,
      ),
    ).toEqual({ column: "name", direction: "asc" });
  });
});

describe("sortUpdates", () => {
  const byName = { column: "name" as Column, direction: "asc" as const };
  it("writes a sort", () => {
    expect(
      sortUpdates<Column>(
        { column: "created", direction: "desc" },
        PAGE_TABLE,
        NO_SORT,
      ),
    ).toEqual({ sort: "created", dir: "desc" });
  });
  it("removes both for no sort", () => {
    expect(sortUpdates<Column>(NO_SORT, PAGE_TABLE, NO_SORT)).toEqual({
      sort: null,
      dir: null,
    });
  });
  it("leaves the default out of the URL", () => {
    expect(sortUpdates(byName, GRANTS, byName)).toEqual({
      "grants.sort": null,
      "grants.dir": null,
    });
  });
});

describe("parseChoice and choiceUpdate", () => {
  const OPTIONS = ["all", "active", "frozen"] as const;
  it("reads an offered choice and falls back on anything else", () => {
    expect(parseChoice("frozen", OPTIONS, "all")).toBe("frozen");
    expect(parseChoice("gone", OPTIONS, "all")).toBe("all");
    expect(parseChoice(null, OPTIONS, "all")).toBe("all");
  });
  it("writes a choice and leaves the default out", () => {
    expect(choiceUpdate("active", "all", "filter")).toEqual({
      filter: "active",
    });
    expect(choiceUpdate("all", "all", "filter")).toEqual({ filter: null });
  });
});
