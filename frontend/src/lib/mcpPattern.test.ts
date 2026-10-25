import { describe, expect, it } from "vitest";

import {
  isPattern,
  matchingEntry,
  patternMatches,
  patternPrefix,
} from "./mcpPattern";

describe("patternMatches", () => {
  it.each([
    ["*", "paladin_list_buckets", true],
    ["*", "", true],
    ["paladin_capability_*", "paladin_capability_issue", true],
    ["paladin_capability_*", "paladin_capability_", true],
    ["paladin_capability_*", "paladin_capabilities", false],
    ["paladin_capability_*", "x_paladin_capability_issue", false],
    ["paladin_create_user", "paladin_create_user", true],
    ["paladin_create_user", "paladin_create_user_x", false],
    // The wildcard counts only at the end, as on the server.
    ["paladin_*_user", "paladin_create_user", false],
    ["paladin_*_user", "paladin_*_user", true],
  ])("%s against %s is %s", (pattern, name, want) => {
    expect(patternMatches(pattern, name)).toBe(want);
  });
});

describe("isPattern and patternPrefix", () => {
  it.each([
    ["paladin_revoke_*", true, "paladin_revoke_"],
    ["*", true, ""],
    ["paladin_create_user", false, "paladin_create_user"],
    ["paladin_*_user", false, "paladin_*_user"],
  ])("%s", (pattern, pattern_, prefix) => {
    expect(isPattern(pattern)).toBe(pattern_);
    expect(patternPrefix(pattern)).toBe(prefix);
  });
});

describe("matchingEntry", () => {
  const DENY = ["paladin_create_user", "paladin_revoke_*", "paladin_*"];

  it("returns the first entry that selects the tool", () => {
    expect(matchingEntry(DENY, "paladin_revoke_token")).toBe(
      "paladin_revoke_*",
    );
    expect(matchingEntry(DENY, "paladin_create_user")).toBe(
      "paladin_create_user",
    );
  });

  it("returns undefined when nothing does", () => {
    expect(matchingEntry(DENY, "other_tool")).toBeUndefined();
    expect(matchingEntry([], "paladin_create_user")).toBeUndefined();
  });
});
