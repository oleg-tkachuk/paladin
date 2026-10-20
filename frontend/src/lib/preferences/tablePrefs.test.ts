import { describe, expect, it } from "vitest";

import {
  defaultSortFor,
  readTablePrefs,
  toggleHidden,
  withTablePrefs,
} from "./tablePrefs";

describe("readTablePrefs", () => {
  it("reads one table's entry", () => {
    expect(readTablePrefs({ users: { density: "compact" } }, "users")).toEqual({
      density: "compact",
    });
  });
  it("reads the view a table is laid out in", () => {
    expect(readTablePrefs({ tenants: { view: "tiles" } }, "tenants")).toEqual({
      view: "tiles",
    });
  });
  it.each([
    ["no map", undefined],
    ["a map of the wrong kind", "x"],
    ["an entry of the wrong shape", { users: { density: "huge" } }],
    ["a view this console does not offer", { users: { view: "kanban" } }],
  ])("reads %s as no preferences", (_, stored) => {
    expect(readTablePrefs(stored, "users")).toEqual({});
  });
});

describe("withTablePrefs", () => {
  it("changes one table and keeps the others, readable or not", () => {
    const stored = { users: { density: "compact" }, other: { future: 1 } };
    expect(
      withTablePrefs(stored, "users", (p) => ({ ...p, hidden: ["tenant"] })),
    ).toEqual({
      users: { density: "compact", hidden: ["tenant"] },
      other: { future: 1 },
    });
  });
  it("drops a table whose preferences are all cleared", () => {
    expect(
      withTablePrefs({ users: { density: "compact" } }, "users", () => ({})),
    ).toEqual({});
  });
});

describe("toggleHidden", () => {
  it("hides and shows a column", () => {
    const hid = toggleHidden({}, "size");
    expect(hid.hidden).toEqual(["size"]);
    expect(toggleHidden(hid, "size").hidden).toBeUndefined();
  });
});

describe("defaultSortFor", () => {
  const COLUMNS = ["name", "created"] as const;
  it("names a column the table sorts by", () => {
    expect(
      defaultSortFor(
        { sort: { column: "created", direction: "desc" } },
        COLUMNS,
      ),
    ).toEqual({ column: "created", direction: "desc" });
  });
  it("is none for a column the table no longer has", () => {
    expect(
      defaultSortFor({ sort: { column: "gone", direction: "asc" } }, COLUMNS),
    ).toBeNull();
  });
});
