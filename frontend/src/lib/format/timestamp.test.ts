import { describe, expect, it } from "vitest";

import { NO_TIMESTAMP, formatTimestampUTC } from "./timestamp";

describe("formatTimestampUTC", () => {
  it("writes the instant in UTC, to the second", () => {
    // 2026-10-01T04:21:15Z
    expect(formatTimestampUTC({ seconds: 1790828475n })).toBe(
      "2026-10-01 04:21:15Z",
    );
  });

  it.each([
    ["absent", undefined],
    ["the zero value", { seconds: 0n }],
    ["beyond what a Date holds", { seconds: 10n ** 15n }],
  ])("writes %s as no timestamp", (_, ts) => {
    expect(formatTimestampUTC(ts)).toBe(NO_TIMESTAMP);
  });
});
