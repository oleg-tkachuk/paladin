import { describe, it, expect } from "vitest";

import { readFileSync } from "node:fs";

import {
  CUSTOM_TEXT_SIZES,
  cn,
  timestampToDate,
  formatDate,
  formatBytes,
} from "./utils";

// The text sizes globals.css defines below Tailwind's scale, read from the
// file itself so a size added there is tested without touching this list.
const DEFINED_TEXT_SIZES = [
  ...readFileSync(
    new URL("../app/globals.css", import.meta.url),
    "utf8",
  ).matchAll(/--text-([a-z]+):/g),
].map((m) => m[1]);

describe("cn", () => {
  it("joins truthy class values and drops falsy ones", () => {
    expect(cn("a", false, undefined, "b", null)).toBe("a b");
  });

  it("resolves conflicting tailwind utilities (last wins)", () => {
    expect(cn("p-2", "p-4")).toBe("p-4");
    expect(cn("text-sm", "text-lg")).toBe("text-lg");
  });

  // tailwind-merge read a size it did not know as a text colour and dropped
  // it beside a real one, so the text rendered at the inherited size.
  it.each(DEFINED_TEXT_SIZES)("keeps text-%s beside a text colour", (size) => {
    expect(cn(`text-${size}`, "text-muted-foreground")).toBe(
      `text-${size} text-muted-foreground`,
    );
  });

  it.each(DEFINED_TEXT_SIZES)("lets a later size replace text-%s", (size) => {
    expect(cn(`text-${size}`, "text-sm")).toBe("text-sm");
  });

  // A size added to globals.css and not here would be dropped again.
  it("knows every text size globals.css defines", () => {
    expect(DEFINED_TEXT_SIZES.length).toBeGreaterThan(0);
    expect([...CUSTOM_TEXT_SIZES].sort()).toEqual(
      [...DEFINED_TEXT_SIZES].sort(),
    );
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
