import { describe, it, expect } from "vitest";

import { asciiLower, celString, searchFilter } from "./cel";

describe("celString", () => {
  it("escapes the characters that could reshape the expression", () => {
    expect(celString("plain")).toBe('"plain"');
    expect(celString('say "hi"')).toBe('"say \\"hi\\""');
    // The case the objects page's `replace(/'/g, …)` got wrong: a trailing
    // backslash escaped the closing quote and the expression stopped parsing.
    expect(celString("trailing\\")).toBe('"trailing\\\\"');
    expect(celString("a\nb")).toBe('"a\\nb"');
  });

  it("cannot be escaped out of", () => {
    // A query engineered to close the literal and append a term. If the
    // encoder ever lets this through, the operator is writing the filter.
    const hostile = '") || (true) || ("';
    const expr = `search.contains(${celString(hostile)})`;
    // Exactly two quote characters bound the literal; every other one is
    // escaped, so the expression has one string in it and not three.
    const unescaped = expr.replace(/\\./g, "").match(/"/g) ?? [];
    expect(unescaped).toHaveLength(2);
  });
});

// The same cases the Go side pins in cel.SearchText's tests, because these are
// two spellings of one definition and drift between them is invisible: the
// console would search for text the server never built.
describe("asciiLower", () => {
  it("folds A-Z and nothing else", () => {
    expect(asciiLower("Prod-LOGS")).toBe("prod-logs");
    expect(asciiLower("ÜBER Cache")).toBe("Über cache");
    expect(asciiLower("İstanbul")).toBe("İstanbul");
    expect(asciiLower("100% DONE")).toBe("100% done");
  });

  it("is not toLowerCase", () => {
    // If this ever stops being true the console and the server disagree about
    // what "ÜBER" means, and the disagreement shows up as a search that finds
    // nothing rather than as an error.
    expect(asciiLower("ÜBER")).not.toBe("ÜBER".toLowerCase());
  });
});

describe("searchFilter", () => {
  it("is one conjunct over the derived field", () => {
    expect(searchFilter("Prod")).toBe('search.contains("prod")');
  });

  it("never emits a disjunction", () => {
    // A disjunction pushes nothing into SQL, so the server would filter one
    // page in memory and report matches past it as absent.
    expect(searchFilter("anything")).not.toContain("||");
  });

  it("returns no filter for an empty query", () => {
    expect(searchFilter("")).toBe("");
    expect(searchFilter("   ")).toBe("");
  });
});
