import { describe, it, expect } from "vitest";
import { create } from "@bufbuild/protobuf";

import { MoneySchema, type Money } from "@/gen/google/type/money_pb";
import {
  ABSTRACT_UNIT_CODE,
  ALLOWED_UNIT_CODES,
  formatMoney,
  isISOCurrency,
  moneyFromDecimal,
  moneyFromNanos,
  moneyIsPositive,
  moneyPercent,
  moneyToDecimal,
  moneyToNanos,
  moneyToNumber,
  unitOf,
} from "./money";

const money = (currencyCode: string, units: bigint, nanos: number): Money =>
  create(MoneySchema, { currencyCode, units, nanos });

describe("unit codes", () => {
  it("mirrors the backend's allowed set, with XXX for no currency", () => {
    expect(ALLOWED_UNIT_CODES).toEqual(["USD", "EUR", "UAH", "GBP", "XXX"]);
    expect(ABSTRACT_UNIT_CODE).toBe("XXX");
  });

  it("renders only fiat codes as currencies", () => {
    expect(isISOCurrency("USD")).toBe(true);
    expect(isISOCurrency("UAH")).toBe(true);
    // A valid ISO code, but not a currency to render with a sign.
    expect(isISOCurrency("XXX")).toBe(false);
    expect(isISOCurrency("UNIT")).toBe(false);
    expect(isISOCurrency("xyz")).toBe(false);
  });

  it("reads an amount's unit, falling back to XXX", () => {
    expect(unitOf(money("EUR", 1n, 0))).toBe("EUR");
    expect(unitOf(money("", 1n, 0))).toBe("XXX");
    expect(unitOf(undefined)).toBe("XXX");
  });
});

describe("moneyFromDecimal", () => {
  it("parses typed decimals exactly, without a float", () => {
    const cases: Array<[string, bigint, number]> = [
      ["0.1", 0n, 100_000_000],
      ["19.99", 19n, 990_000_000],
      [" 25 ", 25n, 0],
      [".5", 0n, 500_000_000],
      ["3.", 3n, 0],
      ["0.000000001", 0n, 1],
      ["999999999.999999999", 999_999_999n, 999_999_999],
    ];
    for (const [text, units, nanos] of cases) {
      expect(moneyFromDecimal(text, "USD")).toMatchObject({
        currencyCode: "USD",
        units,
        nanos,
      });
    }
  });

  it("keeps the unit it is given", () => {
    expect(moneyFromDecimal("1", "XXX")?.currencyCode).toBe("XXX");
  });

  it("refuses what is not a non-negative amount the server counts", () => {
    for (const bad of [
      "",
      ".",
      "-1",
      "1e3",
      "abc",
      "1,5",
      "0.0000000001",
      "1.0000000001",
      "1000000000",
    ]) {
      expect(moneyFromDecimal(bad, "USD")).toBeNull();
    }
  });
});

describe("nanos arithmetic", () => {
  it("adds 0.1 and 0.2 to exactly 0.3", () => {
    const a = moneyFromDecimal("0.1", "USD");
    const b = moneyFromDecimal("0.2", "USD");
    const sum = moneyToNanos(a!) + moneyToNanos(b!);
    expect(sum).toBe(moneyToNanos(moneyFromDecimal("0.3", "USD")!));
    expect(moneyToDecimal(moneyFromNanos(sum, "USD"))).toBe("0.3");
  });

  it("treats an absent amount as zero", () => {
    expect(moneyToNanos(undefined)).toBe(0n);
    expect(moneyToDecimal(undefined)).toBe("0");
    expect(moneyToNumber(undefined)).toBe(0);
  });

  it("splits nanos back into units and nanos", () => {
    expect(moneyFromNanos(1_500_000_000n, "EUR")).toMatchObject({
      currencyCode: "EUR",
      units: 1n,
      nanos: 500_000_000,
    });
  });

  it("round-trips through the form field", () => {
    for (const m of [
      money("USD", 0n, 0),
      money("USD", 0n, 1),
      money("USD", 1n, 500_000_000),
      money("USD", 100n, 0),
      money("USD", 19n, 990_000_000),
      money("USD", 999_999_999n, 999_999_999),
    ]) {
      expect(moneyFromDecimal(moneyToDecimal(m), "USD")).toEqual(m);
    }
    expect(moneyToDecimal(money("USD", 100n, 0))).toBe("100");
    expect(moneyToDecimal(money("USD", 1n, 500_000_000))).toBe("1.5");
    expect(moneyToDecimal(money("USD", 0n, 1))).toBe("0.000000001");
  });

  it("reports whether a budget is set", () => {
    expect(moneyIsPositive(undefined)).toBe(false);
    expect(moneyIsPositive(money("USD", 0n, 0))).toBe(false);
    expect(moneyIsPositive(money("USD", 0n, 1))).toBe(true);
  });

  it("computes a percentage only against a set budget", () => {
    expect(moneyPercent(money("USD", 25n, 0), money("USD", 100n, 0))).toBe(25);
    expect(moneyPercent(money("USD", 1n, 0), money("USD", 0n, 0))).toBeNull();
    expect(moneyPercent(money("USD", 1n, 0), undefined)).toBeNull();
  });
});

describe("formatMoney", () => {
  it("formats an ISO currency with its symbol", () => {
    const out = formatMoney(money("USD", 1234n, 500_000_000), {
      locale: "en-US",
    });
    expect(out).toBe("$1,234.50");
  });

  it("honours an explicit fraction-digit count", () => {
    expect(
      formatMoney(money("USD", 2n, 0), { locale: "en-US", fractionDigits: 2 }),
    ).toBe("$2.00");
    expect(
      formatMoney(money("USD", 0n, 100_000), {
        locale: "en-US",
        fractionDigits: 4,
      }),
    ).toBe("$0.0001");
  });

  it("renders XXX as a plain number of units, never with ¤", () => {
    const out = formatMoney(money("XXX", 1n, 500_000_000), {
      locale: "en-US",
    });
    expect(out).toBe("1.50 units");
    expect(out).not.toContain("¤");
  });

  it("falls back to the given unit, then to XXX, for an amount without one", () => {
    expect(formatMoney(undefined, { locale: "en-US" })).toBe("0.00 units");
    expect(
      formatMoney(money("", 3n, 0), { locale: "en-US", fallbackUnit: "USD" }),
    ).toBe("$3.00");
  });

  it("renders an unknown code verbatim", () => {
    expect(formatMoney(money("ABC", 1n, 0), { locale: "en-US" })).toBe(
      "1.00 ABC",
    );
  });

  it("formats past fifteen significant digits exactly", () => {
    expect(
      formatMoney(money("USD", 999_999_999n, 999_999_999), {
        locale: "en-US",
        fractionDigits: 9,
      }),
    ).toBe("$999,999,999.999999999");
  });
});
