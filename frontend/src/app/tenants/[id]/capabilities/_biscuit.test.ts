import { describe, expect, it } from "vitest";

import { BiscuitStatus } from "@/gen/paladin/admin/v1/capability_service_pb";

import { missingBiscuitReason } from "./_biscuit";

describe("missingBiscuitReason", () => {
  it("says nothing when the Biscuit came", () => {
    expect(missingBiscuitReason(BiscuitStatus.ISSUED)).toBeNull();
  });

  it.each([
    [BiscuitStatus.KEY_BOUND, /bound to a key/],
    [BiscuitStatus.NOT_YET_VALID, /not-before/],
    [BiscuitStatus.UNSPECIFIED, /No Biscuit came/],
  ])("says why status %s came without one", (status, why) => {
    expect(missingBiscuitReason(status)).toMatch(why);
  });
});
