// Previews for the profile's preferences: whether a time zone or a locale is
// one the server would store, and what each looks like. Kept in lib/format
// because these are the one place the console formats in a locale other than
// DISPLAY_LOCALE — the locale preview shows the user's own on purpose.

import { DISPLAY_LOCALE } from "./locale";

/**
 * Whether tz is a zone the server would store: required (apiutil.
 * ValidateTimezone refuses an empty one), and one the runtime knows.
 */
export function isValidTimeZone(tz: string): boolean {
  if (!tz) return false;
  try {
    new Intl.DateTimeFormat(DISPLAY_LOCALE, { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

/** The time at `now` in tz, and its offset: "14:05 · GMT+3". */
export function timeZonePreview(tz: string, now: Date): string | null {
  if (!tz || !isValidTimeZone(tz)) return null;
  const parts = new Intl.DateTimeFormat(DISPLAY_LOCALE, {
    timeZone: tz,
    hour: "2-digit",
    minute: "2-digit",
    timeZoneName: "shortOffset",
  }).formatToParts(now);
  const part = (t: Intl.DateTimeFormatPartTypes) =>
    parts.find((p) => p.type === t)?.value ?? "";
  return `${part("hour")}:${part("minute")} · ${part("timeZoneName")}`;
}

/**
 * Whether tag is a locale the server would store: required (apiutil.
 * ValidateLocale refuses an empty one), and a BCP-47 tag.
 */
export function isValidLocale(tag: string): boolean {
  if (!tag) return false;
  try {
    return Intl.getCanonicalLocales(tag).length === 1;
  } catch {
    return false;
  }
}

// A number with a group and a fraction, so both separators show.
const SAMPLE_NUMBER = 1234567.89;

/** How tag writes a number and a date: "1,234,567.89 · 10/19/2026". */
export function localePreview(tag: string, now: Date): string | null {
  if (!tag || !isValidLocale(tag)) return null;
  const number = new Intl.NumberFormat(tag).format(SAMPLE_NUMBER);
  const date = new Intl.DateTimeFormat(tag, {
    dateStyle: "short",
    timeZone: "UTC",
  }).format(now);
  return `${number} · ${date}`;
}
