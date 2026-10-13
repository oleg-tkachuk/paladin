import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { MoneySchema } from "@/gen/google/type/money_pb";

import {
  budgetCeiling,
  budgetLimit,
  budgetUnit,
  budgetUnlimited,
} from "./budgetCeiling";

const EUR_ONE = create(MoneySchema, { currencyCode: "EUR", units: 1n });
const EUR_ZERO = create(MoneySchema, { currencyCode: "EUR" });

describe("budget ceilings", () => {
  it("writes unlimited and a limit, zero included, on their own arms", () => {
    const open = budgetCeiling(true, EUR_ONE, "UAH");
    expect(open.case).toBe("unlimitedBudget");
    expect(open.value).toMatchObject({ currencyCode: "UAH" });
    expect(budgetCeiling(false, EUR_ZERO, "UAH")).toEqual({
      case: "maxBudget",
      value: EUR_ZERO,
    });
  });

  it("reads a limit of zero as a limit, and unlimited as none", () => {
    const frozen = budgetCeiling(false, EUR_ZERO, "EUR");
    expect(budgetLimit(frozen)).toBe(EUR_ZERO);
    expect(budgetUnlimited(frozen)).toBe(false);
    expect(budgetUnit(frozen)).toBe("EUR");

    const open = budgetCeiling(true, EUR_ONE, "UAH");
    expect(budgetLimit(open)).toBeUndefined();
    expect(budgetUnlimited(open)).toBe(true);
    expect(budgetUnit(open)).toBe("UAH");
  });

  it("knows nothing of a ceiling that is not set", () => {
    for (const c of [undefined, { case: undefined }] as const) {
      expect(budgetLimit(c)).toBeUndefined();
      expect(budgetUnlimited(c)).toBe(false);
      expect(budgetUnit(c)).toBeUndefined();
    }
  });
});
