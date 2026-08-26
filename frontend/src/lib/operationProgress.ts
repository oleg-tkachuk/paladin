// Reading the {processed, total} snapshot the worker leaves on an operation.
//
// The runner writes progress into operations.metadata about once a second
// while an executor loops, and the stale-operation reclaimer copies the last
// snapshot it finds into the failure payload as `last_progress`. Both arrive
// here as an Any wrapping a google.protobuf.Struct, so both are read the same
// way — the only difference is where the struct is nested.
//
// The number is a lower bound, not a count: writes are throttled, so a worker
// that died mid-batch may have finished a few more items than its last report.

import { create } from "@bufbuild/protobuf";
import { anyUnpackTo, StructSchema, type Any } from "@bufbuild/protobuf/wkt";
import type { JsonObject } from "@bufbuild/protobuf";

export interface OpProgress {
  processed: number;
  total: number;
}

/** Decodes an Any-wrapped Struct into a plain object, or null if it is neither. */
export function structFromAny(any: Any | undefined): JsonObject | null {
  if (!any) return null;
  const st = anyUnpackTo(any, StructSchema, create(StructSchema));
  if (!st) return null;
  const out: JsonObject = {};
  for (const [k, v] of Object.entries(st.fields)) {
    if (v.kind.case === "numberValue") out[k] = v.kind.value;
    else if (v.kind.case === "stringValue") out[k] = v.kind.value;
    else if (v.kind.case === "boolValue") out[k] = v.kind.value;
    else if (v.kind.case === "structValue") {
      const nested: JsonObject = {};
      for (const [nk, nv] of Object.entries(v.kind.value.fields)) {
        if (nv.kind.case === "numberValue") nested[nk] = nv.kind.value;
        else if (nv.kind.case === "stringValue") nested[nk] = nv.kind.value;
      }
      out[k] = nested;
    }
  }
  return out;
}

function asProgress(obj: unknown): OpProgress | null {
  if (!obj || typeof obj !== "object") return null;
  const rec = obj as Record<string, unknown>;
  const processed = rec.processed;
  const total = rec.total;
  if (typeof processed !== "number" || typeof total !== "number") return null;
  if (total <= 0) return null;
  return { processed, total };
}

/** Live progress of a running operation, from its metadata. */
export function progressFromMetadata(md: Any | undefined): OpProgress | null {
  return asProgress(structFromAny(md));
}

/**
 * How far a failed operation got, from the `last_progress` the reclaimer
 * copied into the failure payload. Absent for an operation that died before
 * its first report, and for failures the executor itself reported.
 */
export function progressFromFailure(
  details: Any[] | undefined,
): OpProgress | null {
  for (const d of details ?? []) {
    const obj = structFromAny(d);
    const p = asProgress(obj?.last_progress);
    if (p) return p;
  }
  return null;
}
