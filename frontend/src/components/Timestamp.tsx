import {
  NO_TIMESTAMP,
  formatTimestamp,
  formatTimestampUTC,
} from "@/lib/format/timestamp";

/**
 * A protobuf Timestamp in the user's time zone, with the same instant in UTC
 * on hover — the form the server's logs write it in.
 */
export function Timestamp({ ts }: { ts: { seconds: bigint } | undefined }) {
  const local = formatTimestamp(ts);
  if (local === NO_TIMESTAMP) return <>{local}</>;
  const utc = formatTimestampUTC(ts);
  return (
    <time dateTime={utc} title={utc}>
      {local}
    </time>
  );
}
