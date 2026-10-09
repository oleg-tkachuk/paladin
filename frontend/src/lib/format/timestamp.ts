import { formatInstant } from "./locale";

/** What a timestamp the server did not send is written as. */
export const NO_TIMESTAMP = "—";

/** A protobuf Timestamp as a Date, or undefined when absent, zero or out of range. */
function toDate(ts: { seconds: bigint } | undefined): Date | undefined {
  if (!ts) return undefined;
  const ms = Number(ts.seconds) * 1000;
  if (!ms) return undefined;
  const d = new Date(ms);
  // Beyond the range a Date can hold.
  return Number.isNaN(d.getTime()) ? undefined : d;
}

/**
 * A protobuf Timestamp in the user's time zone, to the second, with its
 * offset: 2026-10-01 07:21:15 +03:00.
 *
 * Audit entries, token and capability lifetimes, budgets and the profile are
 * written this way. The offset keeps the instant unambiguous across viewers;
 * formatTimestampUTC gives the same instant as the server's logs write it.
 */
export function formatTimestamp(ts: { seconds: bigint } | undefined): string {
  const d = toDate(ts);
  return d ? formatInstant(d) : NO_TIMESTAMP;
}

/** A protobuf Timestamp in UTC, to the second: 2026-10-01 04:21:15Z. */
export function formatTimestampUTC(
  ts: { seconds: bigint } | undefined,
): string {
  const d = toDate(ts);
  return d
    ? d
        .toISOString()
        .replace("T", " ")
        .replace(/\.\d{3}Z$/, "Z")
    : NO_TIMESTAMP;
}
