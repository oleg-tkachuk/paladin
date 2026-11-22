import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

import {
  MAX_KEY_BYTES,
  MAX_SEGMENT_BYTES,
  keyPrefixProblem,
  objectKeyProblem,
  utf8Bytes,
} from "./objectKey";

// The table the server and both SDKs' fakes are held to as well.
const SHARED = resolve(__dirname, "../../../../sdk/testdata/object_keys.json");

type LengthCase = { unit: string; segments: number[]; canonical: boolean };
const table = JSON.parse(readFileSync(SHARED, "utf8")) as {
  max_key_bytes: number;
  max_segment_bytes: number;
  canonical: string[];
  refused: string[];
  lengths: LengthCase[];
};

/** A lengths case's key: its segments of unit, each to its bytes. */
function lengthKey(c: LengthCase): string {
  const unitBytes = utf8Bytes(c.unit);
  return c.segments
    .map((n) => {
      expect(n % unitBytes).toBe(0);
      return c.unit.repeat(n / unitBytes);
    })
    .join("/");
}

describe("objectKeyProblem", () => {
  it("holds the limits the server does", () => {
    expect(MAX_KEY_BYTES).toBe(table.max_key_bytes);
    expect(MAX_SEGMENT_BYTES).toBe(table.max_segment_bytes);
  });

  it.each(table.canonical)("admits the canonical key %j", (key) => {
    expect(objectKeyProblem(key)).toBeNull();
  });

  it.each(table.refused)("refuses the key %j", (key) => {
    expect(objectKeyProblem(key)).toEqual(expect.any(String));
  });

  it.each(table.lengths)(
    "counts $unit in segments of $segments bytes in UTF-8 (canonical: $canonical)",
    (c) => {
      expect(objectKeyProblem(lengthKey(c)) === null).toBe(c.canonical);
    },
  );

  it("refuses a lone surrogate, which no UTF-8 encodes", () => {
    expect(objectKeyProblem("a\ud800")).toBe("it is not valid UTF-8");
    expect(objectKeyProblem("a\u{1f600}")).toBeNull();
  });

  it("refuses the empty key, which a form must not send", () => {
    expect(objectKeyProblem("")).toBe("it is empty");
  });

  it("names the limit a key is over", () => {
    expect(objectKeyProblem("a".repeat(MAX_KEY_BYTES + 1))).toBe(
      `it is ${MAX_KEY_BYTES + 1} bytes, over the limit of ${MAX_KEY_BYTES}`,
    );
  });
});

describe("keyPrefixProblem", () => {
  it.each([
    ["", null],
    ["backup/", null],
    ["backup/2026/", null],
    ["copy-of-", null],
    ["a/.", null],
    ["a/..", null],
    ["s".repeat(MAX_SEGMENT_BYTES) + "/", null],
    ["s".repeat(MAX_SEGMENT_BYTES - 1), null],
  ])("admits %j", (prefix, want) => {
    expect(keyPrefixProblem(prefix)).toBe(want);
  });

  it.each([
    "/backup/",
    "a//",
    "./a/",
    "a/../",
    "a\tb/",
    "a\ud800/",
    "s".repeat(MAX_SEGMENT_BYTES + 1) + "/",
    "s".repeat(MAX_SEGMENT_BYTES),
    ("s".repeat(MAX_SEGMENT_BYTES) + "/").repeat(3),
  ])("refuses %j", (prefix) => {
    expect(keyPrefixProblem(prefix)).toEqual(expect.any(String));
  });
});
