import { describe, expect, it } from "vitest";

import { liveState, liveTitle } from "./live";

describe("liveState", () => {
  it.each([
    [{ live: false }, "paused"],
    [{ live: false, connected: true }, "paused"],
    [{ live: true }, "poll"],
    [{ live: true, connected: true }, "stream"],
    [{ live: true, connected: false }, "reconnecting"],
  ] as const)("%j is %s", (opts, want) => {
    expect(liveState(opts)).toBe(want);
  });
});

describe("liveTitle", () => {
  it("names the polling interval in seconds", () => {
    expect(liveTitle("poll", 15_000)).toMatch(/every 15 s/);
  });
  it("says a paused view is paused", () => {
    expect(liveTitle("paused")).toMatch(/^Paused/);
  });
});
