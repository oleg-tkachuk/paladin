import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { DurationSchema, timestampDate } from "@bufbuild/protobuf/wkt";

import type { Money } from "@/gen/google/type/money_pb";
import type {
  CapabilityLease,
  CapabilityServiceLeaseRequestSchema,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import {
  moneyFromDecimal,
  moneyFromNanos,
  moneyToNanos,
  NANOS_DECIMALS,
} from "@/lib/format/money";

/**
 * A capability's budget lease (CapabilityService.Lease): budget and requests
 * it holds of its chain in advance, so its charges stop at the lease rather
 * than locking every ancestor and the tenant.
 */

/** A capability's lease as the console reads it: the lease, or none held. */
export type CapabilityLeaseEntry = CapabilityLease | "none";

/** Seconds in a minute, for the lease form's TTL in minutes. */
const SECONDS_PER_MINUTE = 60n;

/** What the lease form holds, as typed. */
export interface LeaseForm {
  /** Budget to lease, a decimal in the capability's unit; "" leases none. */
  amount: string;
  /** Requests to lease, a whole number; "" leases none. */
  requests: string;
  /** How long the lease lasts, in whole minutes; "" is the server's default. */
  ttlMinutes: string;
}

/** The empty lease form. */
export const EMPTY_LEASE_FORM: LeaseForm = {
  amount: "",
  requests: "",
  ttlMinutes: "",
};

const WHOLE_NUMBER = /^\d+$/;

/**
 * The Lease request the form asks for, for capability id in unit, or why it
 * cannot be sent: an amount that is no decimal, requests or a TTL that are
 * no whole number, or nothing to lease at all.
 */
export function leaseRequestOf(
  id: string,
  unit: string,
  form: LeaseForm,
):
  | {
      request: MessageInitShape<typeof CapabilityServiceLeaseRequestSchema>;
      error?: undefined;
    }
  | { error: string } {
  const amountText = form.amount.trim();
  const requestsText = form.requests.trim();
  const ttlText = form.ttlMinutes.trim();
  let amount: Money | undefined;
  if (amountText !== "") {
    const parsed = moneyFromDecimal(amountText, unit);
    if (!parsed) {
      return {
        error: `The amount is a decimal in ${unit}, up to ${NANOS_DECIMALS} decimals.`,
      };
    }
    amount = moneyToNanos(parsed) > 0n ? parsed : undefined;
  }
  if (requestsText !== "" && !WHOLE_NUMBER.test(requestsText)) {
    return { error: "Requests are a whole number." };
  }
  if (ttlText !== "" && !WHOLE_NUMBER.test(ttlText)) {
    return { error: "The duration is a whole number of minutes." };
  }
  const requests = requestsText === "" ? 0n : BigInt(requestsText);
  if (!amount && requests === 0n) {
    return { error: "Lease an amount, requests, or both." };
  }
  const minutes = ttlText === "" ? 0n : BigInt(ttlText);
  return {
    request: {
      id,
      amount,
      requests,
      ttl:
        minutes > 0n
          ? create(DurationSchema, { seconds: minutes * SECONDS_PER_MINUTE })
          : undefined,
    },
  };
}

/** What a lease can still be charged. */
export function leaseRemaining(lease: CapabilityLease): Money {
  const unit = lease.amount?.currencyCode ?? lease.spent?.currencyCode ?? "";
  return moneyFromNanos(
    moneyToNanos(lease.amount) - moneyToNanos(lease.spent),
    unit,
  );
}

/** How many requests a lease can still be bumped. */
export function leaseRequestsRemaining(lease: CapabilityLease): bigint {
  return lease.requests - lease.requestsSpent;
}

/**
 * Whether a lease has run out at now: still held, it is settled by the
 * capability purger, and charges go through the whole chain.
 */
export function leaseExpired(lease: CapabilityLease, now: Date): boolean {
  if (!lease.expiresAt) return false;
  return timestampDate(lease.expiresAt).getTime() <= now.getTime();
}
