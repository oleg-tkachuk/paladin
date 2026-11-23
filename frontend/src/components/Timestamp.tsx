import { Fragment } from "react";

import {
  NO_TIMESTAMP,
  formatTimestamp,
  formatTimestampUTC,
} from "@/lib/format/timestamp";

/** Separates the date, the time and the offset in a formatted timestamp. */
const PART_SEPARATOR = " ";

/**
 * A protobuf Timestamp in the user's time zone, with the same instant in UTC
 * on hover — the form the server's logs write it in. With wrap, a narrow
 * column may put the date, the time and the offset on lines of their own,
 * but never breaks one of them (a browser would break the date at a "-").
 */
export function Timestamp({
  ts,
  wrap = false,
}: {
  ts: { seconds: bigint } | undefined;
  wrap?: boolean;
}) {
  const local = formatTimestamp(ts);
  if (local === NO_TIMESTAMP) return <>{local}</>;
  const utc = formatTimestampUTC(ts);
  return (
    <time dateTime={utc} title={utc}>
      {wrap
        ? local.split(PART_SEPARATOR).map((part, i) => (
            // The separator stays outside the span: inside a nowrap span it
            // is no place to break.
            <Fragment key={i}>
              {i > 0 && PART_SEPARATOR}
              <span className="whitespace-nowrap">{part}</span>
            </Fragment>
          ))
        : local}
    </time>
  );
}
