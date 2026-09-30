import { describe, expect, it } from "vitest";

import { PROVISION_STATE, isProvisionInFlight } from "./bucketProvision";

describe("isProvisionInFlight", () => {
  it.each([PROVISION_STATE.pending, PROVISION_STATE.deleting])(
    "is true for %s",
    (s) => expect(isProvisionInFlight(s)).toBe(true),
  );
  it.each([
    PROVISION_STATE.ready,
    PROVISION_STATE.failed,
    PROVISION_STATE.deletionFailed,
    "",
  ])("is false for %j", (s) => expect(isProvisionInFlight(s)).toBe(false));
});
