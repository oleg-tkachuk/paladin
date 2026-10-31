// How the Budget tab reads a tenant's holds: which ones have lapsed. Pure, so
// the table and its tests agree on the one rule.
import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";

/**
 * Whether a hold is past its expiry at `now`. It still counts against the
 * budget until housekeeping releases it, so the table lists it, marked.
 * A hold with no expiry on the wire is not called lapsed.
 */
export function holdLapsed(
  expiresAt: Timestamp | undefined,
  now: Date,
): boolean {
  return expiresAt !== undefined && timestampDate(expiresAt) <= now;
}
