import { afterEach, describe, expect, it } from "vitest";

import {
  formatCompactCount,
  formatCount,
  formatDateTime,
  formatTime,
  setDisplayTimeZone,
} from "./locale";

// Pinned, so they read the same for every viewer whatever the browser says.
describe("display locale formatters", () => {
  it("groups counts with commas", () => {
    expect(formatCount(1234567)).toBe("1,234,567");
    expect(formatCount(1234567n)).toBe("1,234,567");
  });

  it("writes times in 24-hour form", () => {
    const d = new Date(2026, 9, 1, 16, 18, 33);
    expect(formatTime(d)).toBe("16:18:33");
    expect(formatDateTime(d)).toBe("1 Oct 2026, 16:18:33");
  });

  it("compacts only above 9,999", () => {
    expect(formatCompactCount(9999)).toBe("9,999");
    expect(formatCompactCount(12345)).toBe("12.3k");
  });
});

describe("display time zone", () => {
  const noonUtc = new Date(Date.UTC(2026, 9, 1, 12, 0, 0));
  afterEach(() => {
    setDisplayTimeZone(undefined);
  });

  it("writes times in the zone set", () => {
    expect(setDisplayTimeZone("Asia/Tokyo")).toBe(true);
    expect(formatTime(noonUtc)).toBe("21:00:00");
    expect(formatDateTime(noonUtc)).toBe("1 Oct 2026, 21:00:00");
    setDisplayTimeZone("UTC");
    expect(formatTime(noonUtc)).toBe("12:00:00");
  });

  // The server's tz database can know a zone this browser does not.
  it("falls back to the browser's zone on a name it cannot use", () => {
    setDisplayTimeZone("Asia/Tokyo");
    expect(setDisplayTimeZone("Mars/Olympus_Mons")).toBe(false);
    expect(formatTime(noonUtc)).toBe(
      new Intl.DateTimeFormat("en-GB", { timeStyle: "medium" }).format(noonUtc),
    );
  });
});
