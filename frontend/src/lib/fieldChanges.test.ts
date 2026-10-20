import { describe, expect, it } from "vitest";

import { fieldChanges, fieldValue } from "./fieldChanges";

describe("fieldChanges", () => {
  it("names each leaf that changed by its path", () => {
    expect(
      fieldChanges(
        { name: "b1", versioning: false, quota: { bytes: 1, objects: 2 } },
        { name: "b1", versioning: true, quota: { bytes: 5, objects: 2 } },
      ),
    ).toEqual([
      { kind: "changed", path: "versioning", before: false, after: true },
      { kind: "changed", path: "quota.bytes", before: 1, after: 5 },
    ]);
  });

  it("tells a field added from one removed", () => {
    expect(fieldChanges({ a: 1, b: 2 }, { b: 2, c: 3 })).toEqual([
      { kind: "removed", path: "a", before: 1 },
      { kind: "added", path: "c", after: 3 },
    ]);
  });

  it("quotes a key that is not a name", () => {
    expect(
      fieldChanges(
        { preferences: { "console.diff": { compact: false } } },
        { preferences: { "console.diff": { compact: true } } },
      ),
    ).toEqual([
      {
        kind: "changed",
        path: 'preferences."console.diff".compact',
        before: false,
        after: true,
      },
    ]);
  });

  it("compares an array whole", () => {
    expect(
      fieldChanges({ tags: ["a", "b"] }, { tags: ["x", "a", "b"] }),
    ).toEqual([
      {
        kind: "changed",
        path: "tags",
        before: ["a", "b"],
        after: ["x", "a", "b"],
      },
    ]);
    expect(fieldChanges({ tags: ["a"] }, { tags: ["a"] })).toEqual([]);
  });

  it("reads an object that became a scalar as one change", () => {
    expect(fieldChanges({ limit: { bytes: 1 } }, { limit: null })).toEqual([
      { kind: "changed", path: "limit", before: { bytes: 1 }, after: null },
    ]);
  });

  it("finds nothing between equal documents", () => {
    expect(fieldChanges({ a: { b: [1] } }, { a: { b: [1] } })).toEqual([]);
  });
});

describe("fieldValue", () => {
  it("writes a value as compact JSON", () => {
    expect(fieldValue("on")).toBe('"on"');
    expect(fieldValue({ a: [1, 2] })).toBe('{"a":[1,2]}');
    expect(fieldValue(null)).toBe("null");
  });
});
