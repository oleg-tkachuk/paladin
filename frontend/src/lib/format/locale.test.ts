import { describe, expect, it } from "vitest";

import {
  formatCompactCount,
  formatCount,
  formatDateTime,
  formatTime,
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
