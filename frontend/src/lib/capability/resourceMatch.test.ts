import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { hasDotSegment } from "./resourceMatch";

// limes's prefix matching as a table, which limes and the Python fake are held
// to as well.
const SHARED = resolve(
  __dirname,
  "../../../../sdk/testdata/capability_resource_matches.json",
);
type MatchRow = { prefix: string; uri: string; match: boolean };
const table = JSON.parse(readFileSync(SHARED, "utf8")) as {
  matches: MatchRow[];
};

/** Whether prefix covers uri at a "/" boundary, before the dot-segment rule. */
function atBoundary(prefix: string, uri: string): boolean {
  if (prefix === "" || !uri.startsWith(prefix)) return false;
  return (
    uri.length === prefix.length ||
    prefix.endsWith("/") ||
    uri[prefix.length] === "/"
  );
}

describe("hasDotSegment", () => {
  // Every row the boundary alone would admit is decided by the dot rule.
  const decided = table.matches.filter((r) => atBoundary(r.prefix, r.uri));

  it("has rows to decide", () => {
    expect(decided.some((r) => !r.match)).toBe(true);
  });

  it.each(decided)("reads $uri as limes does", (row) => {
    expect(hasDotSegment(row.uri)).toBe(!row.match);
  });

  it.each([
    ["..", true],
    ["a/./b", true],
    ["%2E%2e/x", true],
    ["..;x/y", true],
    ["..%3bx", true],
    ["%252e%252e", true],
    ["a%2f..", true],
    ["a\\..", true],
    ["..x", false],
    ["a.b/c", false],
    ["%2e%2ex", false],
    ["%", false],
  ])("reads %j as %s", (s, dot) => {
    expect(hasDotSegment(s)).toBe(dot);
  });
});
