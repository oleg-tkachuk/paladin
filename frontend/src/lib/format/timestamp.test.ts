import { afterEach, describe, expect, it } from "vitest";

import { setDisplayTimeZone } from "./locale";
import { NO_TIMESTAMP, formatTimestamp, formatTimestampUTC } from "./timestamp";

// 2026-10-01T04:21:15Z, in Kyiv's summer time (UTC+3).
const SUMMER = { seconds: 1790828475n };
// 2026-12-01T04:21:15Z, in Kyiv's winter time (UTC+2).
const WINTER = { seconds: BigInt(Date.parse("2026-12-01T04:21:15Z") / 1000) };

const ABSENT: [string, { seconds: bigint } | undefined][] = [
  ["absent", undefined],
  ["the zero value", { seconds: 0n }],
  ["beyond what a Date holds", { seconds: 10n ** 15n }],
];

describe("formatTimestamp", () => {
  afterEach(() => {
    setDisplayTimeZone(undefined);
  });

  it.each([
    ["Europe/Kyiv", SUMMER, "2026-10-01 07:21:15 +03:00"],
    ["Europe/Kyiv", WINTER, "2026-12-01 06:21:15 +02:00"],
    ["UTC", SUMMER, "2026-10-01 04:21:15 +00:00"],
    ["America/St_Johns", SUMMER, "2026-10-01 01:51:15 -02:30"],
  ])("writes the instant in %s with its offset", (zone, ts, want) => {
    expect(setDisplayTimeZone(zone)).toBe(true);
    expect(formatTimestamp(ts)).toBe(want);
  });

  it.each(ABSENT)("writes %s as no timestamp", (_, ts) => {
    expect(formatTimestamp(ts)).toBe(NO_TIMESTAMP);
  });
});

describe("formatTimestampUTC", () => {
  it("writes the instant in UTC whatever the display zone", () => {
    setDisplayTimeZone("Europe/Kyiv");
    expect(formatTimestampUTC(SUMMER)).toBe("2026-10-01 04:21:15Z");
    setDisplayTimeZone(undefined);
  });

  it.each(ABSENT)("writes %s as no timestamp", (_, ts) => {
    expect(formatTimestampUTC(ts)).toBe(NO_TIMESTAMP);
  });
});
