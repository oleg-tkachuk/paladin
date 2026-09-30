import { describe, expect, it } from "vitest";

import { bucketNameError } from "./bucketName";

describe("bucketNameError", () => {
  it.each(["paladin-primary", "logs.2026", "abc"])("accepts %s", (name) => {
    expect(bucketNameError(name)).toBeNull();
  });

  it.each(["bad_name", "-leading", "trailing-", ".leading", "UPPER"])(
    "refuses %s by the naming rule",
    (name) => {
      expect(bucketNameError(name)).toMatch(/letters, digits/);
    },
  );

  it.each(["ab", "a".repeat(64)])("refuses %s by length", (name) => {
    expect(bucketNameError(name)).toMatch(/characters/);
  });
});
