import { formatDuration, NO_DURATION, spanMs } from "@/lib/format/duration";
import type { PbTimestamp } from "@/lib/format/duration";

/** Measure a span up to the moment the view renders: a process still open. */
export const UNTIL_NOW = "now";

/**
 * How long a process lasted, from two protobuf Timestamps — or how long it
 * has been running, with `to={UNTIL_NOW}`. A span that cannot be measured
 * reads "—", never "0 ms".
 */
export function Duration({
  from,
  to,
}: {
  from: PbTimestamp | undefined;
  to: PbTimestamp | typeof UNTIL_NOW | undefined;
}) {
  const ms = spanMs(from, to === UNTIL_NOW ? new Date() : to);
  return <>{ms === null ? NO_DURATION : formatDuration(ms)}</>;
}
