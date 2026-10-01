/** What a timestamp the server did not send is written as. */
export const NO_TIMESTAMP = "—";

/**
 * A protobuf Timestamp in UTC, to the second: 2026-10-01 04:21:15Z.
 *
 * Audit entries, token and capability lifetimes, budgets and the profile are
 * written this way: the same instant reads the same for every viewer and
 * matches the server's logs. formatDateTime in ./locale is the other shape,
 * in the viewer's own time zone.
 */
export function formatTimestampUTC(
  ts: { seconds: bigint } | undefined,
): string {
  if (!ts) return NO_TIMESTAMP;
  const ms = Number(ts.seconds) * 1000;
  if (!ms) return NO_TIMESTAMP;
  try {
    return new Date(ms).toISOString().replace("T", " ").replace(".000Z", "Z");
  } catch {
    // Beyond the range a Date can hold.
    return NO_TIMESTAMP;
  }
}
