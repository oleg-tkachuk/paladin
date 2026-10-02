import { describe, it, expect } from "vitest";

import {
  formatMoney,
  fromMicros,
  isISOCurrency,
  MAX_MICROS,
  microsToInput,
  parseMicros,
} from "./money";

describe("isISOCurrency", () => {
  it("accepts ISO codes, rejects the synthetic UNIT and junk", () => {
    expect(isISOCurrency("USD")).toBe(true);
    expect(isISOCurrency("UAH")).toBe(true);
    expect(isISOCurrency("UNIT")).toBe(false);
    expect(isISOCurrency("xyz")).toBe(false);
  });
});

describe("formatMoney", () => {
  it("returns an em-dash for non-finite amounts", () => {
    expect(formatMoney(NaN, "USD")).toBe("—");
    expect(formatMoney(Infinity, "USD")).toBe("—");
  });

  it("formats an ISO currency with its symbol (en-US)", () => {
    const out = formatMoney(1234.5, "USD", "en-US");
    expect(out).toContain("1,234.5");
    expect(out).toContain("$");
  });

  it("honours an explicit fraction-digit count", () => {
    expect(formatMoney(2, "USD", "en-US", 2)).toBe("$2.00");
  });
});

describe("micros", () => {
  it("parses typed decimals exactly, without a float", () => {
    expect(parseMicros("0.1")).toBe(100_000n);
    expect(parseMicros("19.99")).toBe(19_990_000n);
    expect(parseMicros(" 25 ")).toBe(25_000_000n);
    expect(parseMicros(".5")).toBe(500_000n);
    expect(parseMicros("3.")).toBe(3_000_000n);
    expect(parseMicros("0.000001")).toBe(1n);
  });

  it("refuses what is not a non-negative amount it can count", () => {
    for (const bad of ["", ".", "-1", "1e3", "abc", "1.0000001", "1,5"]) {
      expect(parseMicros(bad)).toBeNull();
    }
    expect(parseMicros("999999999.999999")).toBe(MAX_MICROS);
    expect(parseMicros("1000000000")).toBeNull();
  });

  it("round-trips through the form field", () => {
    for (const m of [
      0n,
      1n,
      1_500_000n,
      100_000_000n,
      19_990_000n,
      MAX_MICROS,
    ]) {
      expect(parseMicros(microsToInput(m))).toBe(m);
    }
    expect(microsToInput(100_000_000n)).toBe("100");
    expect(microsToInput(1_500_000n)).toBe("1.5");
  });

  it("converts for display", () => {
    expect(fromMicros(undefined)).toBe(0);
    expect(fromMicros(19_990_000n)).toBe(19.99);
  });
});
