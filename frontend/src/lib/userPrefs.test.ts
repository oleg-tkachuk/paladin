import { describe, expect, it } from "vitest";

import { NEW_PASSWORD_MIN_LENGTH, timeZoneOptions } from "./userPrefs";
import {
  isValidLocale,
  isValidTimeZone,
  localePreview,
  timeZonePreview,
} from "./format/preferences";

// 2026-10-19 11:05 UTC: Kyiv is on summer time (UTC+3) until 25 October.
const NOW = new Date(Date.UTC(2026, 9, 19, 11, 5));

describe("timeZoneOptions", () => {
  it("lists every zone the runtime knows, UTC first and once", () => {
    const zones = timeZoneOptions();
    expect(zones[0]).toBe("UTC");
    expect(zones.filter((z) => z === "UTC")).toHaveLength(1);
    expect(zones).toContain("Europe/London");
    expect(zones.length).toBeGreaterThan(100);
  });
});

describe("isValidTimeZone", () => {
  it.each([
    ["", false],
    ["UTC", true],
    ["Europe/Kyiv", true],
    ["Europe/Kiev", true],
    ["Mars/Olympus", false],
    ["kyiv", false],
  ])("%j → %s", (tz, want) => {
    expect(isValidTimeZone(tz)).toBe(want);
  });
});

describe("timeZonePreview", () => {
  it("says the time there and its offset", () => {
    expect(timeZonePreview("Europe/Kyiv", NOW)).toBe("14:05 · GMT+3");
    expect(timeZonePreview("UTC", NOW)).toMatch(/^11:05 · GMT(\+0)?$/);
  });

  it("says nothing of an unset or unknown zone", () => {
    expect(timeZonePreview("", NOW)).toBeNull();
    expect(timeZonePreview("Mars/Olympus", NOW)).toBeNull();
  });
});

describe("isValidLocale", () => {
  it.each([
    ["", false],
    ["en-US", true],
    ["uk-UA", true],
    ["en_US", false],
    ["not a locale", false],
  ])("%j → %s", (tag, want) => {
    expect(isValidLocale(tag)).toBe(want);
  });
});

describe("localePreview", () => {
  it("shows how the locale writes a number and a date", () => {
    expect(localePreview("en-US", NOW)).toBe("1,234,567.89 · 10/19/26");
    expect(localePreview("de-DE", NOW)).toBe("1.234.567,89 · 19.10.26");
  });

  it("says nothing of an unset or invalid tag", () => {
    expect(localePreview("", NOW)).toBeNull();
    expect(localePreview("en_US", NOW)).toBeNull();
  });
});

describe("NEW_PASSWORD_MIN_LENGTH", () => {
  it("is the floor the contract declares", () => {
    // auth_service.proto: new_password min_len = 12.
    expect(NEW_PASSWORD_MIN_LENGTH).toBe(12);
  });
});
