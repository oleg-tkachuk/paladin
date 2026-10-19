// The billing page's period: a preset or a custom range of days, turned into
// the instants the ledger is asked for, and the granularities a chart of it
// can be drawn at.

import { STEP_MS, type Granularity } from "./spendSeries";

export type PresetKey = "7d" | "30d" | "90d" | "custom";

export const PRESETS: { key: PresetKey; label: string; days: number | null }[] =
  [
    { key: "7d", label: "7 days", days: 7 },
    { key: "30d", label: "30 days", days: 30 },
    { key: "90d", label: "90 days", days: 90 },
    { key: "custom", label: "Custom", days: null },
  ];

export const DEFAULT_PRESET: PresetKey = "30d";

export const GRANULARITIES: { value: Granularity; label: string }[] = [
  { value: "hour", label: "Hourly" },
  { value: "day", label: "Daily" },
  { value: "week", label: "Weekly" },
];

/**
 * The most bars a chart is drawn with. Past this one bar is a pixel or two
 * and the chart reads as noise, so a finer granularity is not offered.
 */
export const MAX_CHART_BARS = 200;

const DAY_MS = STEP_MS.day;

export interface Period {
  start: Date;
  end: Date;
}

/**
 * The period a preset or a custom range names, as of `now`; or why it names
 * none. A custom range is whole UTC days, both ends included.
 */
export function resolvePeriod(
  preset: PresetKey,
  customStart: string,
  customEnd: string,
  now: Date,
): Period | { error: string } {
  if (preset !== "custom") {
    const days = PRESETS.find((p) => p.key === preset)?.days ?? 0;
    return { start: new Date(now.getTime() - days * DAY_MS), end: now };
  }
  if (!customStart || !customEnd) return { error: "Choose both dates." };
  const start = new Date(`${customStart}T00:00:00Z`);
  const end = new Date(new Date(`${customEnd}T00:00:00Z`).getTime() + DAY_MS);
  if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) {
    return { error: "Not a date." };
  }
  if (start >= end) return { error: "The start is after the end." };
  if (start > now) return { error: "The period has not started yet." };
  return { start, end: end > now ? now : end };
}

/** Whether a resolved period is one, rather than the reason it is not. */
export function isPeriod(p: Period | { error: string }): p is Period {
  return "start" in p;
}

/** The granularities a chart of the period can be drawn at. */
export function allowedGranularities(p: Period): Granularity[] {
  const span = p.end.getTime() - p.start.getTime();
  return GRANULARITIES.map((g) => g.value).filter(
    (g) => Math.ceil(span / STEP_MS[g]) <= MAX_CHART_BARS,
  );
}

/** The granularity to draw at: the one wanted, else the finest allowed. */
export function pickGranularity(p: Period, wanted: Granularity): Granularity {
  const allowed = allowedGranularities(p);
  return allowed.includes(wanted) ? wanted : (allowed[0] ?? "week");
}
