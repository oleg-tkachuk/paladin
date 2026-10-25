import { describe, expect, it } from "vitest";

import { changedFields, parseState, stateChange } from "./stateChanges";

const bytes = (v: unknown) => new TextEncoder().encode(JSON.stringify(v));

describe("changedFields", () => {
  it("names each field that differs by its path, and leaves the rest", () => {
    expect(
      changedFields(
        {
          name: "tenants/t",
          display_name: "Old",
          labels: { team: "a" },
          kept: 1,
        },
        {
          name: "tenants/t",
          display_name: "New",
          labels: { team: "a", env: "prod" },
          kept: 1,
        },
      ),
    ).toEqual([
      {
        path: "display_name",
        kind: "changed",
        before: '"Old"',
        after: '"New"',
      },
      { path: "labels.env", kind: "added", after: '"prod"' },
    ]);
  });

  it("indexes a list's items, and names an item gone", () => {
    expect(changedFields({ rules: ["a", "b"] }, { rules: ["a"] })).toEqual([
      { path: "rules[1]", kind: "removed", before: '"b"' },
    ]);
  });

  it("finds nothing between equal states", () => {
    expect(changedFields({ a: { b: [1] } }, { a: { b: [1] } })).toEqual([]);
  });
});

describe("stateChange", () => {
  it("is none without either state", () => {
    expect(stateChange(undefined, new Uint8Array())).toEqual({ kind: "none" });
  });

  it("is a resource made when only the after is recorded", () => {
    expect(stateChange(undefined, bytes({ a: 1 }))).toEqual({
      kind: "created",
      after: { a: 1 },
    });
  });

  it("is a resource removed when only the before is recorded", () => {
    expect(stateChange(bytes({ a: 1 }), undefined)).toEqual({
      kind: "removed",
      before: { a: 1 },
    });
  });

  it("lists the fields changed between the two", () => {
    expect(stateChange(bytes({ a: 1 }), bytes({ a: 2 }))).toEqual({
      kind: "changed",
      fields: [{ path: "a", kind: "changed", before: "1", after: "2" }],
    });
  });
});

describe("parseState", () => {
  it("reads nothing from what does not parse", () => {
    expect(parseState(new TextEncoder().encode("{not json"))).toBeUndefined();
  });
});
