import { describe, expect, it } from "vitest";

import { OBJECT_ORDER, isObjectOrderField } from "./objectOrder";

describe("isObjectOrderField", () => {
  it.each(Object.values(OBJECT_ORDER))("accepts %s", (f) => {
    expect(isObjectOrderField(f)).toBe(true);
  });

  // Names the objects page once sent, and the server now refuses.
  it.each(["status", "object_tag", "", "id", "Key"])("refuses %j", (f) => {
    expect(isObjectOrderField(f)).toBe(false);
  });
});
