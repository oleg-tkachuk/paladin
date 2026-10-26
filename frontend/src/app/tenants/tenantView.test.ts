import { describe, expect, it } from "vitest";
import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import { MoneySchema } from "@/gen/google/type/money_pb";
import { TenantBudgetSchema } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import { TenantSchema } from "@/gen/paladin/admin/v1/types_pb";
import type { TenantLimitSignals } from "@/lib/tenant/limits";

import { plainLabels, tenantBreakdowns, tenantStatus } from "./tenantView";

const tenant = (over: MessageInitShape<typeof TenantSchema> = {}) =>
  create(TenantSchema, { tenantId: "t", slug: "t", ...over });

describe("tenantStatus", () => {
  it("is active by default", () => {
    expect(tenantStatus(tenant())).toEqual({
      label: "Active",
      tone: "success",
    });
  });

  it("says a tenant whose notice is loud needs a look", () => {
    expect(
      tenantStatus(
        tenant({
          labels: {
            "notice.paladin.io/content": "x",
            "notice.paladin.io/severity": "critical",
          },
        }),
      ),
    ).toEqual({ label: "Critical notice", tone: "danger" });
    expect(
      tenantStatus(
        tenant({
          labels: {
            "notice.paladin.io/content": "x",
            "notice.paladin.io/severity": "warning",
          },
        }),
      ).tone,
    ).toBe("warning");
    // An info notice is news, not a state.
    expect(
      tenantStatus(tenant({ labels: { "notice.paladin.io/content": "x" } })),
    ).toEqual({ label: "Active", tone: "success" });
  });

  it("is frozen in the trash, whatever its notice", () => {
    expect(
      tenantStatus(
        tenant({
          deletedAt: timestampFromDate(new Date()),
          labels: {
            "notice.paladin.io/content": "x",
            "notice.paladin.io/severity": "critical",
          },
        }),
      ),
    ).toEqual({ label: "Frozen", tone: "suspended" });
  });
});

describe("tenantStatus with limit signals", () => {
  const usd = (units: bigint) =>
    create(MoneySchema, { currencyCode: "USD", units });
  const budget = (spent: bigint) =>
    create(TenantBudgetSchema, {
      tenantId: "t",
      spent: usd(spent),
      budgetCeiling: { case: "maxBudget", value: usd(100n) },
    });
  const notice = (severity: string) => ({
    "notice.paladin.io/content": "x",
    "notice.paladin.io/severity": severity,
  });
  const OVER: TenantLimitSignals = { budget: budget(100n) };
  const AT_QUOTA: TenantLimitSignals = { quota: "at-limit" };
  const NEAR_QUOTA: TenantLimitSignals = { quota: "near-limit" };
  const NEAR_BUDGET: TenantLimitSignals = { budget: budget(85n) };

  // Frozen, then the most urgent tone of notice and limit, the notice first
  // at the same tone, then Active.
  it.each<
    [string, Record<string, string>, TenantLimitSignals | undefined, string]
  >([
    ["no signals", {}, undefined, "Active"],
    ["nothing listed", {}, {}, "Active"],
    ["over budget", {}, OVER, "Over budget"],
    ["at quota", {}, AT_QUOTA, "At quota"],
    ["near quota", {}, NEAR_QUOTA, "Near quota"],
    ["near budget", {}, NEAR_BUDGET, "Near budget"],
    [
      "a critical notice over over budget",
      notice("critical"),
      OVER,
      "Critical notice",
    ],
    [
      "over budget over a warning notice",
      notice("warning"),
      OVER,
      "Over budget",
    ],
    [
      "a critical notice over near quota",
      notice("critical"),
      NEAR_QUOTA,
      "Critical notice",
    ],
    [
      "a warning notice over near quota",
      notice("warning"),
      NEAR_QUOTA,
      "Warning notice",
    ],
    [
      "near quota over an info notice",
      { "notice.paladin.io/content": "x" },
      NEAR_QUOTA,
      "Near quota",
    ],
  ])("%s", (_, labels, limits, want) => {
    expect(tenantStatus(tenant({ labels }), limits).label).toBe(want);
  });

  it("is frozen in the trash, whatever its limits", () => {
    expect(
      tenantStatus(tenant({ deletedAt: timestampFromDate(new Date()) }), OVER),
    ).toEqual({ label: "Frozen", tone: "suspended" });
  });

  it("carries the limit state's tone", () => {
    expect(tenantStatus(tenant(), OVER).tone).toBe("danger");
    expect(tenantStatus(tenant(), NEAR_QUOTA).tone).toBe("warning");
  });
});

describe("plainLabels", () => {
  it("leaves a notice's labels out", () => {
    expect(
      plainLabels({ team: "storage", "notice.paladin.io/content": "x" }),
    ).toEqual([["team", "storage"]]);
  });
});

describe("tenantBreakdowns", () => {
  it("splits the tenants by layout, default backend and label key", () => {
    const tenants = [
      tenant({
        storageLayout: "dedicated",
        defaultBucket: "storageBackends/primary/buckets/paladin-t1",
        labels: { team: "a", env: "prod" },
      }),
      tenant({
        storageLayout: "shared",
        defaultBucket: "storageBackends/primary/buckets/pool",
        labels: { team: "b", "notice.paladin.io/content": "x" },
      }),
      tenant({ storageLayout: "" }),
    ];
    expect(tenantBreakdowns(tenants)).toEqual({
      layout: [
        { key: "Shared", count: 2 },
        { key: "Dedicated", count: 1 },
      ],
      backend: [
        { key: "primary", count: 2 },
        { key: "No default bucket", count: 1 },
      ],
      labels: [
        { key: "team", count: 2 },
        { key: "env", count: 1 },
      ],
    });
  });
});
