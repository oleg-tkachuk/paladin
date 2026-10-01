// Pure formatting helpers shared by the object version row, details dialog,
// and the tab's restore copy. No React. Extracted from ObjectVersionsTab.
import type { ObjectVersion } from "@/gen/paladin/data/v1/object_service_pb";
import { formatDateTime } from "@/lib/format/locale";

export function formatTimestampSeconds(seconds: bigint | undefined): string {
  if (!seconds) return "—";
  try {
    return formatDateTime(new Date(Number(seconds) * 1000));
  } catch {
    return "—";
  }
}

export function shortId(value: string, head = 10): string {
  if (!value) return "";
  if (value.length <= head + 3) return value;
  return `${value.slice(0, head)}…`;
}

export function checksumDisplay(
  cs: ObjectVersion["checksum"],
  truncate = true,
): string {
  if (!cs || !cs.algorithm) return "—";
  const algo = cs.algorithm.toLowerCase();
  const val = truncate ? shortId(cs.value, 16) : cs.value;
  return `${algo}:${val}`;
}
