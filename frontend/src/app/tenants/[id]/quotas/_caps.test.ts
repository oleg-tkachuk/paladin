import { describe, expect, it } from "vitest";

import { capFieldOf, capOf, capPercent } from "./_caps";

const ZERO = BigInt(0);

describe("quota caps", () => {
  it("reads no cap as Unlimited and a cap of 0 as a limit", () => {
    expect(capFieldOf(undefined)).toEqual({ value: "", unlimited: true });
    expect(capFieldOf(ZERO)).toEqual({ value: "0", unlimited: false });
  });

  it("sends Unlimited as no cap, and 0 as a cap", () => {
    expect(capOf({ value: "5", unlimited: true })).toBeUndefined();
    expect(capOf({ value: "0", unlimited: false })).toBe(ZERO);
    expect(capOf({ value: " 42 ", unlimited: false })).toBe(BigInt(42));
  });

  it("holds a limit that is not a whole number", () => {
    for (const value of ["", "-1", "1.5", "1e3", "ten"]) {
      expect(capOf({ value, unlimited: false })).toBeNull();
    }
  });

  it("counts a cap of 0 as full", () => {
    expect(capPercent(ZERO, ZERO)).toBe(100);
    expect(capPercent(BigInt(50), BigInt(200))).toBe(25);
    expect(capPercent(BigInt(300), BigInt(200))).toBe(100);
  });
});
