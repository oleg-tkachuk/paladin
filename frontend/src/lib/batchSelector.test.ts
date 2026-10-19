import { describe, expect, it } from "vitest";

import {
  SELECTOR_MAX_NAMES,
  copyTargetProblem,
  selectorBatches,
} from "./batchSelector";

describe("SELECTOR_MAX_NAMES", () => {
  it("is the cap the contract puts on a selector's names", () => {
    expect(SELECTOR_MAX_NAMES).toBe(100);
  });
});

describe("selectorBatches", () => {
  const item = (collection: string, n: number) => ({
    collection,
    name: `${collection}/o${n}`,
  });

  it("groups by collection, keeping the order", () => {
    expect(selectorBatches([item("a", 1), item("b", 1), item("a", 2)])).toEqual(
      [
        { collection: "a", names: ["a/o1", "a/o2"] },
        { collection: "b", names: ["b/o1"] },
      ],
    );
  });

  it("splits a collection past the cap", () => {
    const items = [1, 2, 3, 4, 5].map((n) => item("a", n));
    expect(selectorBatches(items, 2).map((b) => b.names.length)).toEqual([
      2, 2, 1,
    ]);
  });

  it("splits at the contract's cap by default", () => {
    const items = Array.from({ length: SELECTOR_MAX_NAMES + 1 }, (_, n) =>
      item("a", n),
    );
    expect(selectorBatches(items).map((b) => b.names.length)).toEqual([
      SELECTOR_MAX_NAMES,
      1,
    ]);
  });

  it("makes no batch of nothing", () => {
    expect(selectorBatches([])).toEqual([]);
  });
});

describe("copyTargetProblem", () => {
  it.each([
    ["another collection, no prefix", "docs", "archive", "", null],
    ["the same collection under a prefix", "docs", "docs", "v2/", null],
  ])("allows %s", (_, src, dst, prefix, want) => {
    expect(copyTargetProblem(src, dst, prefix)).toBe(want);
  });
  it.each([
    ["no destination", "docs", "", ""],
    ["the same collection without a prefix", "docs", "docs", ""],
  ])("refuses %s", (_, src, dst, prefix) => {
    expect(copyTargetProblem(src, dst, prefix)).toEqual(expect.any(String));
  });
});
