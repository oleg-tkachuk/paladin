import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { MoneySchema } from "@/gen/google/type/money_pb";
import { BudgetUnreadableSchema } from "@/gen/paladin/admin/v1/billing_service_pb";

import {
  BudgetState,
  budgetCeiling,
  budgetLimit,
  budgetState,
  budgetUnit,
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
    expect(budgetState(frozen)).toBe(BudgetState.Limited);
    expect(budgetUnit(frozen)).toBe("EUR");

    const open = budgetCeiling(true, EUR_ONE, "UAH");
    expect(budgetLimit(open)).toBeUndefined();
    expect(budgetState(open)).toBe(BudgetState.Unlimited);
    expect(budgetUnit(open)).toBe("UAH");
  });

  it("knows nothing of a ceiling that is not set", () => {
    for (const c of [undefined, { case: undefined }] as const) {
      expect(budgetLimit(c)).toBeUndefined();
      expect(budgetState(c)).toBe(BudgetState.None);
      expect(budgetUnit(c)).toBeUndefined();
    }
  });

  it("reads an unreadable budget as neither a limit, unlimited nor none", () => {
    const unknown = {
      case: "budgetUnreadable",
      value: create(BudgetUnreadableSchema),
    } as const;
    expect(budgetLimit(unknown)).toBeUndefined();
    expect(budgetUnit(unknown)).toBeUndefined();
    expect(budgetState(unknown)).toBe(BudgetState.Unreadable);
  });

  it.each([
    [budgetCeiling(false, EUR_ZERO, "EUR"), BudgetState.Limited],
    [budgetCeiling(true, EUR_ONE, "EUR"), BudgetState.Unlimited],
    [{ case: undefined } as const, BudgetState.None],
    [undefined, BudgetState.None],
  ])("names the state of %o %s", (c, want) => {
    expect(budgetState(c)).toBe(want);
  });
});
