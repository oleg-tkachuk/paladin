import { describe, it, expect } from "vitest";

import { formatMoney, isISOCurrency } from "./money";

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
