import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { MoneySchema } from "@/gen/google/type/money_pb";
import { CapabilityCaveatsSchema } from "@/gen/paladin/admin/v1/capability_service_pb";

import {
  budgetCeiling,
  budgetLimit,
  budgetUnit,
  requestCeiling,
  requestLimit,
} from "./_ceilings";

const EUR_ONE = create(MoneySchema, { currencyCode: "EUR", units: 1n });
const ZERO = BigInt(0);
const SEVEN = BigInt(7);

describe("capability ceilings", () => {
  it("writes unlimited and a limit, 0 included, on their own arms", () => {
    expect(requestCeiling(true, SEVEN)).toEqual({
      case: "unlimitedRequests",
      value: true,
    });
    expect(requestCeiling(false, ZERO)).toEqual({
      case: "maxRequests",
      value: ZERO,
    });
    const open = budgetCeiling(true, EUR_ONE, "UAH");
    expect(open.case).toBe("unlimitedBudget");
    expect(open.value).toMatchObject({ currencyCode: "UAH" });
    expect(budgetCeiling(false, EUR_ONE, "UAH")).toEqual({
      case: "maxBudget",
      value: EUR_ONE,
    });
  });

  it("reads a limit of 0 as a limit, and unlimited as none", () => {
    const frozen = create(CapabilityCaveatsSchema, {
      requestCeiling: requestCeiling(false, ZERO),
      budgetCeiling: budgetCeiling(
        false,
        create(MoneySchema, { currencyCode: "EUR" }),
        "EUR",
      ),
    });
    expect(requestLimit(frozen)).toBe(ZERO);
    expect(budgetLimit(frozen)?.units).toBe(ZERO);
    expect(budgetUnit(frozen)).toBe("EUR");

    const open = create(CapabilityCaveatsSchema, {
      requestCeiling: requestCeiling(true, ZERO),
      budgetCeiling: budgetCeiling(true, EUR_ONE, "UAH"),
    });
    expect(requestLimit(open)).toBeNull();
    expect(budgetLimit(open)).toBeUndefined();
    expect(budgetUnit(open)).toBe("UAH");
  });

  it("knows nothing of caveats that set neither", () => {
    const unset = create(CapabilityCaveatsSchema, {});
    expect(requestLimit(unset)).toBeNull();
    expect(budgetLimit(unset)).toBeUndefined();
    expect(budgetUnit(unset)).toBeUndefined();
    expect(requestLimit(undefined)).toBeNull();
  });
});
