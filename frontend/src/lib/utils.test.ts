import { describe, it, expect } from "vitest";

import { cn, timestampToDate, formatDate, formatBytes } from "./utils";

describe("cn", () => {
  it("joins truthy class values and drops falsy ones", () => {
    expect(cn("a", false, undefined, "b", null)).toBe("a b");
  });

  it("resolves conflicting tailwind utilities (last wins)", () => {
    expect(cn("p-2", "p-4")).toBe("p-4");
    expect(cn("text-sm", "text-lg")).toBe("text-lg");
  });
});

describe("timestampToDate", () => {
  it("returns current time for null/undefined", () => {
    expect(timestampToDate(null)).toBeInstanceOf(Date);
    expect(timestampToDate(undefined)).toBeInstanceOf(Date);
  });

  it("combines seconds and nanos into epoch millis", () => {
    expect(
      timestampToDate({ seconds: 1000, nanos: 500_000_000 }).getTime(),
    ).toBe(1_000_500);
  });

  it("accepts bigint seconds", () => {
    expect(timestampToDate({ seconds: 2n }).getTime()).toBe(2000);
  });
});

describe("formatDate", () => {
  it("returns N/A when the date is undefined", () => {
    expect(formatDate(undefined)).toBe("N/A");
  });

  it("renders the coarsest non-zero unit relative to now", () => {
    const ago = (ms: number) => new Date(Date.now() - ms);
    expect(formatDate(ago(5_000))).toBe("5s ago");
    expect(formatDate(ago(3 * 60_000))).toBe("3m ago");
    expect(formatDate(ago(2 * 3_600_000))).toBe("2h ago");
    expect(formatDate(ago(4 * 86_400_000))).toBe("4d ago");
  });
});

describe("formatBytes", () => {
  it("returns 0 Bytes for zero", () => {
    expect(formatBytes(0)).toBe("0 Bytes");
  });

  it("scales into the right unit with default 2 decimals", () => {
    expect(formatBytes(1024)).toBe("1 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1_048_576)).toBe("1 MB");
  });

  it("accepts bigint and honours a custom decimals arg", () => {
    expect(formatBytes(1_073_741_824n)).toBe("1 GB");
    expect(formatBytes(1536, 0)).toBe("2 KB");
  });
});
