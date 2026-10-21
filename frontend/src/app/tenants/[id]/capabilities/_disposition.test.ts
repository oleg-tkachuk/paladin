import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import {
  CapabilityDisposition,
  CapabilitySchema,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import {
  DEFAULT_STATUSES,
  STATUS_FILTERS,
  capabilityStatus,
  isCapabilityRevoked,
  toggleStatus,
} from "./_disposition";

const { ACTIVE, EXPIRED, REVOKED, UNSPECIFIED } = CapabilityDisposition;
const HOUR_MS = 60 * 60 * 1000;

describe("capabilityStatus", () => {
  it.each([
    [ACTIVE, "active", "success"],
    [EXPIRED, "expired", "outline"],
    [REVOKED, "revoked", "destructive"],
  ])("reads disposition %s as %s", (disposition, label, variant) => {
    // An expiry still ahead: the disposition decides, not the clock.
    const c = create(CapabilitySchema, {
      disposition,
      expiresAt: timestampFromDate(new Date(Date.now() + HOUR_MS)),
    });
    expect(capabilityStatus(c)).toEqual({ label, variant });
  });

  // Get answers no disposition; its expiry is all it says.
  it("falls back to the expiry when there is no disposition", () => {
    const past = create(CapabilitySchema, {
      disposition: UNSPECIFIED,
      expiresAt: timestampFromDate(new Date(Date.now() - HOUR_MS)),
    });
    const ahead = create(CapabilitySchema, {
      expiresAt: timestampFromDate(new Date(Date.now() + HOUR_MS)),
    });
    expect(capabilityStatus(past).label).toBe("expired");
    expect(capabilityStatus(ahead).label).toBe("active");
  });
});

describe("toggleStatus", () => {
  it("adds and removes a status in the filter's order", () => {
    expect(toggleStatus([REVOKED], ACTIVE, true)).toEqual([ACTIVE, REVOKED]);
    expect(toggleStatus([ACTIVE, EXPIRED], ACTIVE, false)).toEqual([EXPIRED]);
  });

  // No status would ask the server for its default, the active ones alone.
  it("keeps the last status on", () => {
    expect(toggleStatus([EXPIRED], EXPIRED, false)).toEqual([EXPIRED]);
  });

  it("starts on the active capabilities, and filters by all three", () => {
    expect(DEFAULT_STATUSES).toEqual([ACTIVE]);
    expect(STATUS_FILTERS.map((f) => f.disposition)).toEqual([
      ACTIVE,
      EXPIRED,
      REVOKED,
    ]);
  });
});

describe("isCapabilityRevoked", () => {
  it("follows the disposition the list names", () => {
    expect(
      isCapabilityRevoked(create(CapabilitySchema, { disposition: REVOKED })),
    ).toBe(true);
    // A listed active one is active, whatever a record says.
    expect(
      isCapabilityRevoked(
        create(CapabilitySchema, { disposition: ACTIVE }),
        true,
      ),
    ).toBe(false);
  });

  it("reads one Get answered by its own revocation", () => {
    const got = create(CapabilitySchema, { disposition: UNSPECIFIED });
    expect(isCapabilityRevoked(got, true)).toBe(true);
    expect(isCapabilityRevoked(got)).toBe(false);
    expect(capabilityStatus(got, true).label).toBe("revoked");
  });
});
