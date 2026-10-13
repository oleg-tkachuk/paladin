import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { CapabilityCaveatsSchema } from "@/gen/paladin/admin/v1/capability_service_pb";

import { requestCeiling, requestLimit } from "./_ceilings";

const ZERO = BigInt(0);
const SEVEN = BigInt(7);

describe("capability request ceilings", () => {
  it("writes unlimited and a limit, 0 included, on their own arms", () => {
    expect(requestCeiling(true, SEVEN)).toEqual({
      case: "unlimitedRequests",
      value: true,
    });
    expect(requestCeiling(false, ZERO)).toEqual({
      case: "maxRequests",
      value: ZERO,
    });
  });

  it("reads a limit of 0 as a limit, and unlimited as none", () => {
    const frozen = create(CapabilityCaveatsSchema, {
      requestCeiling: requestCeiling(false, ZERO),
    });
    expect(requestLimit(frozen)).toBe(ZERO);
    const open = create(CapabilityCaveatsSchema, {
      requestCeiling: requestCeiling(true, ZERO),
    });
    expect(requestLimit(open)).toBeNull();
  });

  it("knows nothing of caveats that set neither", () => {
    expect(requestLimit(create(CapabilityCaveatsSchema, {}))).toBeNull();
    expect(requestLimit(undefined)).toBeNull();
  });
});
