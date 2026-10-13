// The time axis of a spend chart.
//
// GetTenantTimeSeries returns only the buckets that hold a charge — the SQL
// groups by date_trunc, so a day with nothing billed has no row. Drawn as is,
// three charges a week apart sit side by side and read as three busy days in
// a row. The axis is filled here instead: every bucket of the period, in
// order, the empty ones at zero.
//
// Buckets are aligned in UTC, as the server's date_trunc aligns them; a week
// starts on Monday, as date_trunc('week') does.

export type Granularity = "hour" | "day" | "week";

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;
const WEEK_MS = 7 * DAY_MS;
// getUTCDay() numbers Sunday 0; date_trunc's week starts on Monday.
const MONDAY = 1;
const DAYS_PER_WEEK = 7;

export const STEP_MS: Record<Granularity, number> = {
  hour: HOUR_MS,
  day: DAY_MS,
  week: WEEK_MS,
};

/** The start of the bucket `t` falls in, as date_trunc(granularity) puts it. */
export function bucketStart(t: Date, granularity: Granularity): Date {
  const d = new Date(t.getTime());
  if (granularity === "hour") {
    d.setUTCMinutes(0, 0, 0);
    return d;
  }
  d.setUTCHours(0, 0, 0, 0);
  if (granularity === "week") {
    const sinceMonday =
      (d.getUTCDay() - MONDAY + DAYS_PER_WEEK) % DAYS_PER_WEEK;
    d.setUTCDate(d.getUTCDate() - sinceMonday);
  }
  return d;
}

export interface SpendPoint {
  start: Date;
  /** Spend in the bucket, as a number for drawing; exact values stay in `spent`. */
  value: number;
  chargeCount: number;
  /** The bucket's spend as the server sent it, for display. */
  spent?: unknown;
}

/**
 * Every bucket from the one holding `from` to the one holding `to`, inclusive,
 * with the server's points in their places and zero elsewhere. A point outside
 * the range is kept, so nothing the server reported is dropped.
 */
export function fillSeries(
  points: SpendPoint[],
  from: Date,
  to: Date,
  granularity: Granularity,
): SpendPoint[] {
  const step = STEP_MS[granularity];
  const byStart = new Map(points.map((p) => [p.start.getTime(), p]));
  const out: SpendPoint[] = [];
  const last = bucketStart(to, granularity).getTime();
  for (let t = bucketStart(from, granularity).getTime(); t <= last; t += step) {
    out.push(
      byStart.get(t) ?? { start: new Date(t), value: 0, chargeCount: 0 },
    );
    byStart.delete(t);
  }
  return [...out, ...byStart.values()].sort(
    (a, b) => a.start.getTime() - b.start.getTime(),
  );
}

/** A tick for a value axis: max rounded up to 1, 2 or 5 × a power of ten. */
export function niceMax(max: number): number {
  if (!(max > 0)) return 1;
  const magnitude = 10 ** Math.floor(Math.log10(max));
  for (const m of [1, 2, 5, 10]) {
    if (m * magnitude >= max) return m * magnitude;
  }
  return 10 * magnitude;
}
