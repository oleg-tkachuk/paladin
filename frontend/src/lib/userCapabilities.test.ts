import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "vitest";

import {
  UserCapabilitySchema,
  UserCapability_State,
} from "@/gen/paladin/iam/v1/user_service_pb";

import {
  myCapabilitiesHref,
  statesFor,
  userCapabilityLifecycle,
} from "./userCapabilities";

describe("userCapabilities", () => {
  it("links to the user's capabilities, or one grant's", () => {
    expect(myCapabilitiesHref()).toBe("/my-capabilities");
    expect(myCapabilitiesHref("g-1")).toBe("/my-capabilities?grant=g-1");
  });

  it.each([
    [UserCapability_State.ACTIVE, "active"],
    [UserCapability_State.REVOKED, "revoked"],
    [UserCapability_State.EXPIRED, "expired"],
    // A server too old to say lists the active ones alone.
    [UserCapability_State.UNSPECIFIED, "active"],
  ])("reads state %s as %s", (state, want) => {
    expect(
      userCapabilityLifecycle(create(UserCapabilitySchema, { state })),
    ).toBe(want);
  });

  it("asks for the states chosen", () => {
    expect(statesFor(["revoked", "expired"])).toEqual([
      UserCapability_State.REVOKED,
      UserCapability_State.EXPIRED,
    ]);
  });

  // The server reads no states as the active ones; no choice here is all.
  it("asks for every state when none is chosen", () => {
    expect(statesFor([])).toEqual([
      UserCapability_State.ACTIVE,
      UserCapability_State.REVOKED,
      UserCapability_State.EXPIRED,
    ]);
  });

  it("ignores a choice it does not know, as from an edited URL", () => {
    expect(statesFor(["bogus", "active"])).toEqual([
      UserCapability_State.ACTIVE,
    ]);
    expect(statesFor(["bogus"])).toHaveLength(3);
  });
});
