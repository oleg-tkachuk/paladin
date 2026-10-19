import { describe, expect, it } from "vitest";

import { emptyMessage, emptyReason } from "./emptyState";

describe("emptyReason", () => {
  it.each([
    [{ pending: true, filtered: true }, "pending"],
    [{ filtered: true }, "filtered"],
    [{}, "empty"],
  ] as const)("%j is %s", (opts, want) => {
    expect(emptyReason(opts)).toBe(want);
  });
});

describe("emptyMessage", () => {
  it.each([
    ["pending", "Searching…"],
    ["filtered", "No buckets match your filters."],
    ["empty", "No buckets yet."],
  ] as const)("%s says %s", (reason, want) => {
    expect(emptyMessage(reason, "buckets")).toBe(want);
  });
});
