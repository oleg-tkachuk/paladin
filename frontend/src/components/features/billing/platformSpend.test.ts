import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { TenantSpendSchema } from "@/gen/paladin/admin/v1/billing_service_pb";

import { platformTotals, spendShare } from "./platformSpend";

const spent = (currencyCode: string, units: bigint, charges: bigint) =>
  create(TenantSpendSchema, {
    tenantId: crypto.randomUUID(),
    spent: { currencyCode, units, nanos: 0 },
    chargeCount: charges,
  });

describe("platformTotals", () => {
  // The server lists the platform's currency first; another unit does not
  // add to it, but its charges still count.
  it("totals the leading unit, and counts every charge", () => {
    const tenants = [
      spent("USD", 6n, 3n),
      spent("USD", 2n, 1n),
      spent("EUR", 50n, 9n),
    ];
    const totals = platformTotals(tenants, "XXX");
    expect(totals.total).toMatchObject({ currencyCode: "USD", units: 8n });
    expect(totals.charges).toBe(13);
    // 8 over the four charges in USD; the EUR ones are not in it.
    expect(totals.perCharge).toMatchObject({ currencyCode: "USD", units: 2n });
    expect(totals.tenants).toBe(3);
    expect(totals.otherUnit).toBe(1);
    expect(spendShare(tenants[0], totals)).toBe(75);
    expect(spendShare(tenants[2], totals)).toBeNull();
  });

  it("is the fallback unit and nothing for no tenant", () => {
    const totals = platformTotals([], "XXX");
    expect(totals.total).toMatchObject({ currencyCode: "XXX", units: 0n });
    expect(totals).toMatchObject({ charges: 0, tenants: 0, otherUnit: 0 });
  });
});
