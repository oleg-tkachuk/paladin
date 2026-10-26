import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import { MoneySchema } from "@/gen/google/type/money_pb";
import {
  TenantBudgetSchema,
  type TenantBudget,
} from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import { UnlimitedBudgetSchema } from "@/gen/paladin/admin/v1/types_pb";

import {
  limitSignalsByTenant,
  tenantLimitState,
  type TenantLimitSignals,
} from "./limits";

const usd = (units: bigint, nanos = 0) =>
  create(MoneySchema, { currencyCode: "USD", units, nanos });

/** A budget capped at `cap` units with `spent` spent. */
const capped = (spent: bigint, cap: bigint, spentNanos = 0): TenantBudget =>
  create(TenantBudgetSchema, {
    tenantId: "t",
    spent: usd(spent, spentNanos),
    budgetCeiling: { case: "maxBudget", value: usd(cap) },
  });

const unlimited = create(TenantBudgetSchema, {
  tenantId: "t",
  spent: usd(5n),
  budgetCeiling: {
    case: "unlimitedBudget",
    value: create(UnlimitedBudgetSchema, { currencyCode: "USD" }),
  },
});

// Over budget is told from the spend and the cap in exact nanos, not from
// utilisation_pct, which is capped at 100 for display.
describe("tenantLimitState of a budget past the threshold", () => {
  it.each<[string, TenantBudget, string]>([
    ["under its cap", capped(79n, 100n), "Near budget"],
    // A nano short is not the cap: the capped percent would round to 100.
    ["a nano under its cap", capped(99n, 100n, 999_999_999), "Near budget"],
    ["at its cap", capped(100n, 100n), "Over budget"],
    ["past its cap", capped(120n, 100n), "Over budget"],
    ["a cap of zero, which allows no spend", capped(0n, 0n), "Over budget"],
    ["unlimited", unlimited, "Near budget"],
    [
      "no ceiling the console knows",
      create(TenantBudgetSchema, { tenantId: "t", spent: usd(5n) }),
      "Near budget",
    ],
  ])("%s", (_, budget, want) => {
    expect(tenantLimitState({ budget })?.label).toBe(want);
  });
});

describe("tenantLimitState", () => {
  it.each<[string, TenantLimitSignals | undefined, string | undefined]>([
    ["no reads", undefined, undefined],
    ["nothing listed", {}, undefined],
    ["a quota near its cap", { quota: "near-limit" }, "Near quota"],
    ["a quota at its cap", { quota: "at-limit" }, "At quota"],
    ["a budget near its cap", { budget: capped(85n, 100n) }, "Near budget"],
    ["a budget at its cap", { budget: capped(100n, 100n) }, "Over budget"],
    // Precedence: a reached cap before a near one, budget before quota.
    [
      "over budget and at quota",
      { quota: "at-limit", budget: capped(150n, 100n) },
      "Over budget",
    ],
    [
      "at quota and near budget",
      { quota: "at-limit", budget: capped(85n, 100n) },
      "At quota",
    ],
    [
      "near quota and near budget",
      { quota: "near-limit", budget: capped(85n, 100n) },
      "Near quota",
    ],
    [
      "over budget and near quota",
      { quota: "near-limit", budget: capped(100n, 100n) },
      "Over budget",
    ],
  ])("%s", (_, signals, want) => {
    expect(tenantLimitState(signals)?.label).toBe(want);
  });

  it("tells a reached cap as danger and a near one as a warning", () => {
    expect(tenantLimitState({ quota: "at-limit" })?.tone).toBe("danger");
    expect(tenantLimitState({ budget: capped(100n, 100n) })?.tone).toBe(
      "danger",
    );
    expect(tenantLimitState({ quota: "near-limit" })?.tone).toBe("warning");
    expect(tenantLimitState({ budget: capped(85n, 100n) })?.tone).toBe(
      "warning",
    );
  });
});

describe("limitSignalsByTenant", () => {
  it("joins the reads per tenant id", () => {
    const over = capped(100n, 100n);
    const near = capped(85n, 100n);
    const got = limitSignalsByTenant({
      quotaAtLimit: ["a"],
      quotaNearLimit: ["b", "a"],
      budgets: [
        { tenantId: "b", budget: over },
        { tenantId: "c", budget: near },
        // A summary without a budget row tells nothing.
        { tenantId: "d" },
      ],
    });
    expect([...got.keys()].sort()).toEqual(["a", "b", "c"]);
    // At a quota cap wins over near another, whatever order the reads came.
    expect(got.get("a")).toEqual({ quota: "at-limit" });
    expect(got.get("b")).toEqual({ quota: "near-limit", budget: over });
    expect(got.get("c")).toEqual({ budget: near });
  });

  it("keeps the last row of a tenant Summarize returned twice", () => {
    const first = capped(85n, 100n);
    const last = capped(100n, 100n);
    const got = limitSignalsByTenant({
      quotaAtLimit: [],
      quotaNearLimit: [],
      budgets: [
        { tenantId: "a", budget: first },
        { tenantId: "a", budget: last },
      ],
    });
    expect(got.get("a")?.budget).toBe(last);
  });

  it("is empty when every read is", () => {
    expect(
      limitSignalsByTenant({
        quotaAtLimit: [],
        quotaNearLimit: [],
        budgets: [],
      }).size,
    ).toBe(0);
  });
});
