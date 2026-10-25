// How long something took, in the largest units that read naturally:
// "850 ms", "12 s", "3 min 4 s", "2 h 5 min", "7 d 2 h".

const MS_PER_SECOND = 1000;
const SECONDS_PER_MINUTE = 60;
const MINUTES_PER_HOUR = 60;
const HOURS_PER_DAY = 24;
const NANOS_PER_MS = 1_000_000;

/** What a duration reads when it has no length to show. */
export const NO_DURATION = "—";

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return NO_DURATION;
  if (ms < MS_PER_SECOND) return `${Math.round(ms)} ms`;
  const s = Math.round(ms / MS_PER_SECOND);
  if (s < SECONDS_PER_MINUTE) return `${s} s`;
  const m = Math.floor(s / SECONDS_PER_MINUTE);
  if (m < MINUTES_PER_HOUR) {
    const rest = s % SECONDS_PER_MINUTE;
    return rest ? `${m} min ${rest} s` : `${m} min`;
  }
  const h = Math.floor(m / MINUTES_PER_HOUR);
  if (h < HOURS_PER_DAY) {
    const rest = m % MINUTES_PER_HOUR;
    return rest ? `${h} h ${rest} min` : `${h} h`;
  }
  const d = Math.floor(h / HOURS_PER_DAY);
  const rest = h % HOURS_PER_DAY;
  return rest ? `${d} d ${rest} h` : `${d} d`;
}

/** A protobuf Timestamp as the generated types carry it. */
export type PbTimestamp = { seconds: bigint | number; nanos?: number };

const toMs = (ts: PbTimestamp) =>
  Number(ts.seconds) * MS_PER_SECOND + (ts.nanos ?? 0) / NANOS_PER_MS;

/**
 * The milliseconds from start to end, or null when either end is missing or
 * the end comes first — a span that cannot be measured, not a zero one.
 */
export function spanMs(
  start: PbTimestamp | undefined,
  end: PbTimestamp | Date | undefined,
): number | null {
  if (!start || !end) return null;
  const ms = (end instanceof Date ? end.getTime() : toMs(end)) - toMs(start);
  return ms < 0 ? null : ms;
}
