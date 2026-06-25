import { describe, expect, it } from "vitest";
import { detectDelimiter, parseCSV } from "./csv";

describe("detectDelimiter", () => {
  it("prefers tab when present, else comma", () => {
    expect(detectDelimiter("a\tb\tc")).toBe("\t");
    expect(detectDelimiter("a,b,c")).toBe(",");
    // Tab wins even if commas are also present.
    expect(detectDelimiter("a,b\tc")).toBe("\t");
  });
});

describe("parseCSV", () => {
  it("returns [] for empty / whitespace-only input", () => {
    expect(parseCSV("")).toEqual([]);
    expect(parseCSV("   \n  \n")).toEqual([]);
  });

  it("keys each row by the trimmed header cells", () => {
    expect(parseCSV(" name , size \nfoo,10")).toEqual([
      { name: "foo", size: "10" },
    ]);
  });

  it("respects double-quoted cells containing the delimiter", () => {
    expect(parseCSV('name,note\n"a,b",ok')).toEqual([
      { name: "a,b", note: "ok" },
    ]);
  });

  it("unescapes doubled quotes inside a quoted cell", () => {
    expect(parseCSV('note\n"he said ""hi"""')).toEqual([
      { note: 'he said "hi"' },
    ]);
  });

  it("parses tab-delimited input", () => {
    expect(parseCSV("a\tb\n1\t2")).toEqual([{ a: "1", b: "2" }]);
  });

  it("normalizes CRLF and drops trailing blank lines", () => {
    expect(parseCSV("a,b\r\n1,2\r\n\r\n")).toEqual([{ a: "1", b: "2" }]);
  });

  it("fills missing trailing cells with empty strings", () => {
    expect(parseCSV("a,b,c\n1,2")).toEqual([{ a: "1", b: "2", c: "" }]);
  });

  it("parses multiple rows", () => {
    expect(parseCSV("a,b\n1,2\n3,4")).toEqual([
      { a: "1", b: "2" },
      { a: "3", b: "4" },
    ]);
  });
});
