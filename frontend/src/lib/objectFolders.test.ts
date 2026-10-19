import { describe, expect, it } from "vitest";

import { folderName, folderPrefix, folderTrail } from "./objectFolders";

describe("folderPrefix", () => {
  it.each([
    [null, ""],
    ["", ""],
    ["/", ""],
    ["a", "a/"],
    ["a/b/", "a/b/"],
    ["//a/b", "a/b/"],
  ])("%s lists at %s", (raw, want) => {
    expect(folderPrefix(raw)).toBe(want);
  });
});

describe("folderTrail", () => {
  it("runs from the root to the folder", () => {
    expect(folderTrail("a/b/")).toEqual([
      { name: "", prefix: "" },
      { name: "a", prefix: "a/" },
      { name: "b", prefix: "a/b/" },
    ]);
  });
  it("is the root alone at the root", () => {
    expect(folderTrail("")).toEqual([{ name: "", prefix: "" }]);
  });
});

describe("folderName", () => {
  it("names a folder by its own segment", () => {
    expect(folderName("a/b/", "a/")).toBe("b");
    expect(folderName("top/", "")).toBe("top");
  });
});
