import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, it } from "vitest";

import { CapabilityLeaseSchema } from "@/gen/paladin/admin/v1/capability_service_pb";

import {
  EMPTY_LEASE_FORM,
  leaseExpired,
  leaseRemaining,
  leaseRequestOf,
  leaseRequestsRemaining,
} from "./capabilityLease";

const ID = "c-1";
const UNIT = "EUR";
const form = (f: Partial<typeof EMPTY_LEASE_FORM>) => ({
  ...EMPTY_LEASE_FORM,
  ...f,
});

describe("leaseRequestOf", () => {
  it("leases an amount in the capability's unit, exactly", () => {
    const r = leaseRequestOf(ID, UNIT, form({ amount: "2.5" }));
    expect(r.error).toBeUndefined();
    if (r.error !== undefined) return;
    expect(r.request.id).toBe(ID);
    expect(r.request.amount).toMatchObject({
      currencyCode: UNIT,
      units: 2n,
      nanos: 500_000_000,
    });
    expect(r.request.requests).toBe(0n);
    expect(r.request.ttl).toBeUndefined();
  });

  it("leases requests alone, for minutes given", () => {
    const r = leaseRequestOf(
      ID,
      UNIT,
      form({ requests: "10", ttlMinutes: "3" }),
    );
    if (r.error !== undefined) throw new Error(r.error);
    expect(r.request.amount).toBeUndefined();
    expect(r.request.requests).toBe(10n);
    expect(r.request.ttl?.seconds).toBe(180n);
  });

  it("reads a zero amount and zero minutes as none", () => {
    const r = leaseRequestOf(
      ID,
      UNIT,
      form({ amount: "0", requests: "1", ttlMinutes: "0" }),
    );
    if (r.error !== undefined) throw new Error(r.error);
    expect(r.request.amount).toBeUndefined();
    expect(r.request.ttl).toBeUndefined();
  });

  it.each([
    ["nothing", form({}), "Lease an amount"],
    ["a zero amount alone", form({ amount: "0" }), "Lease an amount"],
    ["a negative amount", form({ amount: "-1" }), "decimal in EUR"],
    ["too many decimals", form({ amount: "0.0000000001" }), "decimal in EUR"],
    ["fractional requests", form({ requests: "1.5" }), "whole number"],
    ["fractional minutes", form({ amount: "1", ttlMinutes: "0.5" }), "minutes"],
  ])("refuses %s", (_, f, want) => {
    expect(leaseRequestOf(ID, UNIT, f).error).toContain(want);
  });
});

describe("a lease's balance", () => {
  const lease = create(CapabilityLeaseSchema, {
    amount: { currencyCode: UNIT, units: 5n },
    spent: { currencyCode: UNIT, units: 1n, nanos: 250_000_000 },
    requests: 10n,
    requestsSpent: 4n,
    expiresAt: timestampFromDate(new Date("2026-11-01T12:00:00Z")),
  });

  it("is what it holds less what was spent", () => {
    expect(leaseRemaining(lease)).toMatchObject({
      currencyCode: UNIT,
      units: 3n,
      nanos: 750_000_000,
    });
    expect(leaseRequestsRemaining(lease)).toBe(6n);
  });

  it("runs out at its end", () => {
    expect(leaseExpired(lease, new Date("2026-11-01T11:59:59Z"))).toBe(false);
    expect(leaseExpired(lease, new Date("2026-11-01T12:00:00Z"))).toBe(true);
    expect(leaseExpired(create(CapabilityLeaseSchema, {}), new Date())).toBe(
      false,
    );
  });
});
