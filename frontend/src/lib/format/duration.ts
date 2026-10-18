// How long something took, in the largest units that read naturally:
// "850 ms", "12 s", "3 min 4 s", "2 h 5 min".

const MS_PER_SECOND = 1000;
const SECONDS_PER_MINUTE = 60;
const MINUTES_PER_HOUR = 60;

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < MS_PER_SECOND) return `${Math.round(ms)} ms`;
  const s = Math.round(ms / MS_PER_SECOND);
  if (s < SECONDS_PER_MINUTE) return `${s} s`;
  const m = Math.floor(s / SECONDS_PER_MINUTE);
  if (m < MINUTES_PER_HOUR) {
    const rest = s % SECONDS_PER_MINUTE;
    return rest ? `${m} min ${rest} s` : `${m} min`;
  }
  const h = Math.floor(m / MINUTES_PER_HOUR);
  const rest = m % MINUTES_PER_HOUR;
  return rest ? `${h} h ${rest} min` : `${h} h`;
}
