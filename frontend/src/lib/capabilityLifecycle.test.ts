import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  CapabilityDisposition,
  CapabilitySchema,
} from "@/gen/paladin/admin/v1/capability_service_pb";

import { capabilityLifecycle, dispositionsFor } from "./capabilityLifecycle";

describe("capabilityLifecycle", () => {
  it.each([
    [CapabilityDisposition.ACTIVE, "active"],
    [CapabilityDisposition.REVOKED, "revoked"],
    [CapabilityDisposition.EXPIRED, "expired"],
    [CapabilityDisposition.UNSPECIFIED, "active"],
  ])("reads disposition %s as %s", (disposition, want) => {
    expect(capabilityLifecycle(create(CapabilitySchema, { disposition }))).toBe(
      want,
    );
  });
});

describe("dispositionsFor", () => {
  it("asks for the states chosen", () => {
    expect(dispositionsFor(["expired"])).toEqual([
      CapabilityDisposition.EXPIRED,
    ]);
  });

  // The server reads none as the active ones; no choice here is every one.
  it("asks for every disposition when none is chosen, or none it knows", () => {
    const every = [
      CapabilityDisposition.ACTIVE,
      CapabilityDisposition.REVOKED,
      CapabilityDisposition.EXPIRED,
    ];
    expect(dispositionsFor([])).toEqual(every);
    expect(dispositionsFor(["bogus"])).toEqual(every);
  });
});
