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
 * A locale saved in the user's settings does not override it: comparable
 * output across operators is the point.
 *
 * Times are in the time zone the user saved, or the browser's until they save
 * one (setDisplayTimeZone).
 */
export const DISPLAY_LOCALE = "en-GB";

const COUNT = new Intl.NumberFormat(DISPLAY_LOCALE);

function dateTimeFormats(timeZone: string | undefined) {
  return {
    dateTime: new Intl.DateTimeFormat(DISPLAY_LOCALE, {
      dateStyle: "medium",
      timeStyle: "medium",
      timeZone,
    }),
    time: new Intl.DateTimeFormat(DISPLAY_LOCALE, {
      timeStyle: "medium",
      timeZone,
    }),
    instant: new Intl.DateTimeFormat(DISPLAY_LOCALE, {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hourCycle: "h23",
      timeZoneName: "longOffset",
      timeZone,
    }),
  };
}

let dates = dateTimeFormats(undefined);
let datesZone: string | undefined;

/**
 * Writes times in `timeZone` (an IANA name) from now on; undefined returns to
 * the browser's. A name this browser does not know is ignored rather than
 * thrown on: the server validates against its own tz database, which can be
 * newer. Returns whether the zone took effect.
 */
export function setDisplayTimeZone(timeZone: string | undefined): boolean {
  if (timeZone === datesZone) return true;
  try {
    dates = dateTimeFormats(timeZone);
    datesZone = timeZone;
    return true;
  } catch {
    dates = dateTimeFormats(undefined);
    datesZone = undefined;
    return false;
  }
}

/** A count: 1,234,567. */
export function formatCount(n: number | bigint): string {
  return COUNT.format(n);
}

/** A point in time, to the second: 1 Oct 2026, 16:18:33. */
export function formatDateTime(d: Date): string {
  return dates.dateTime.format(d);
}

/** A time of day, to the second: 16:18:33. */
export function formatTime(d: Date): string {
  return dates.time.format(d);
}

// What longOffset names the zone by: "GMT+03:00", or "GMT" alone at a zero
// offset in some engines.
const OFFSET_PREFIX = "GMT";
const ZERO_OFFSET = "+00:00";

/**
 * A point in time as an operator compares it: 2026-10-01 07:21:15 +03:00.
 * Sortable, with the offset written out, so a time read in one zone can be
 * matched against another viewer's or the server's UTC logs.
 */
export function formatInstant(d: Date): string {
  const part: Partial<Record<Intl.DateTimeFormatPartTypes, string>> = {};
  for (const { type, value } of dates.instant.formatToParts(d)) {
    part[type] = value;
  }
  const offset =
    (part.timeZoneName ?? "").replace(OFFSET_PREFIX, "") || ZERO_OFFSET;
  return `${part.year}-${part.month}-${part.day} ${part.hour}:${part.minute}:${part.second} ${offset}`;
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
