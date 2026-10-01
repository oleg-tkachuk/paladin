/**
 * The one locale the console renders numbers and times in.
 *
 * `toLocaleString()` with no argument takes the VIEWER's locale, so the same
 * count read 1,234,567 on one screen and 1 234 567 on the next, and the same
 * timestamp read 4:18:33 PM or 16:18:33 — on audit trails, quotas and object
 * versions an operator compares across screens and with a colleague.
 *
 * en-GB: thousands grouped with commas, 24-hour time, and the month as a word,
 * so a date is not read day-first by one reader and month-first by another.
 * Times stay in the viewer's own time zone; only how they are written is fixed.
 */
export const DISPLAY_LOCALE = "en-GB";

const COUNT = new Intl.NumberFormat(DISPLAY_LOCALE);
const DATE_TIME = new Intl.DateTimeFormat(DISPLAY_LOCALE, {
  dateStyle: "medium",
  timeStyle: "medium",
});
const TIME = new Intl.DateTimeFormat(DISPLAY_LOCALE, { timeStyle: "medium" });

/** A count: 1,234,567. */
export function formatCount(n: number | bigint): string {
  return COUNT.format(n);
}

/** A point in time, to the second: 1 Oct 2026, 16:18:33. */
export function formatDateTime(d: Date): string {
  return DATE_TIME.format(d);
}

/** A time of day, to the second: 16:18:33. */
export function formatTime(d: Date): string {
  return TIME.format(d);
}

// Above this, a KPI tile writes 12.3k rather than 12,345, which would push the
// tile out of its grid cell.
const COMPACT_ABOVE = 9999;
const COMPACT = new Intl.NumberFormat(DISPLAY_LOCALE, {
  notation: "compact",
  maximumFractionDigits: 1,
});

/** A count for a tight space: 12.3k above 9,999, otherwise as formatCount. */
export function formatCompactCount(n: number | bigint): string {
  const v = typeof n === "bigint" ? Number(n) : n;
  return v > COMPACT_ABOVE ? COMPACT.format(v) : formatCount(v);
}
