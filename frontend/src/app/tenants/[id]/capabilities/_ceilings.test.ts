import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";

import { CapabilityCaveatsSchema } from "@/gen/paladin/admin/v1/capability_service_pb";

import {
  copyCeiling,
  copyLimit,
  issuableRequestLimit,
  MIN_MAX_REQUESTS,
  requestCeiling,
  requestLimit,
  wholeNumber,
} from "./_ceilings";

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

describe("capability copy quotas", () => {
  it("writes unlimited and a quota, 0 included, on their own arms", () => {
    expect(copyCeiling(true, SEVEN)).toEqual({
      case: "unlimitedCopies",
      value: true,
    });
    expect(copyCeiling(false, ZERO)).toEqual({
      case: "maxCopies",
      value: ZERO,
    });
  });

  it("reads a quota of 0 as a quota, and unlimited or unset as none", () => {
    const frozen = create(CapabilityCaveatsSchema, {
      copyCeiling: copyCeiling(false, ZERO),
    });
    expect(copyLimit(frozen)).toBe(ZERO);
    const open = create(CapabilityCaveatsSchema, {
      copyCeiling: copyCeiling(true, ZERO),
    });
    expect(copyLimit(open)).toBeNull();
    expect(copyLimit(create(CapabilityCaveatsSchema, {}))).toBeNull();
    expect(copyLimit(undefined)).toBeNull();
  });
});

describe("whole numbers", () => {
  it("reads digits, trimmed, and nothing else", () => {
    expect(wholeNumber(" 7 ")).toBe(SEVEN);
    expect(wholeNumber("0")).toBe(ZERO);
    for (const text of ["", "-1", "1.5", "1e3", "seven"]) {
      expect(wholeNumber(text)).toBeNull();
    }
  });
});

describe("request limits an operator may issue", () => {
  it("takes a whole number from the least the server issues", () => {
    expect(issuableRequestLimit(String(MIN_MAX_REQUESTS))).toBe(
      MIN_MAX_REQUESTS,
    );
    expect(issuableRequestLimit(" 1000 ")).toBe(BigInt(1000));
  });

  it("refuses a limit no request could use, and what is no number", () => {
    for (const text of ["0", "-3", "1.5", "", "many"]) {
      expect(issuableRequestLimit(text)).toBeNull();
    }
  });
});
