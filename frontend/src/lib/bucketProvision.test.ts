import { describe, expect, it } from "vitest";

import {
  PROVISION_STATE,
  PROVISION_STATES,
  isProvisionInFlight,
  provisionStatus,
  provisionView,
} from "./bucketProvision";

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

describe("provisionStatus", () => {
  it.each([
    [PROVISION_STATE.ready, "Ready", "success"],
    // A bucket from before the outbox reads as ready, as its badge does.
    ["", "Ready", "success"],
    [PROVISION_STATE.pending, "Provisioning", "progress"],
    [PROVISION_STATE.deleting, "Deleting", "progress"],
    [PROVISION_STATE.failed, "Provisioning failed", "danger"],
    [PROVISION_STATE.deletionFailed, "Deletion failed", "danger"],
    ["mystery", "mystery", "unknown"],
  ])("%j → %s", (state, label, tone) => {
    expect(provisionStatus(state)).toEqual({ label, tone });
  });
});

describe("provisionView", () => {
  it("names, tones and explains every state the server sends", () => {
    expect([...PROVISION_STATES].sort()).toEqual(
      Object.values(PROVISION_STATE).sort(),
    );
    for (const state of PROVISION_STATES) {
      const v = provisionView(state);
      expect(v.tone).not.toBe("unknown");
      expect(v.label).not.toBe(state);
      expect(v.detail).not.toBe("");
    }
  });

  it("shows a state it does not know as the server spells it", () => {
    expect(provisionView("mystery")).toMatchObject({
      label: "mystery",
      tone: "unknown",
    });
  });
});
