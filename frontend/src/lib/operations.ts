// What the console says about a long-running operation: where it stands, what
// it was asked to do, and how it ended. One reading of the Operation message,
// shared by every place that shows one.

import { toJson } from "@bufbuild/protobuf";
import { anyUnpackTo, StructSchema, type Any } from "@bufbuild/protobuf/wkt";
import { create } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";

import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";

export type OpStatus =
  "pending" | "running" | "succeeded" | "failed" | "cancelled";

const FROM_STATE: Partial<Record<OperationState, OpStatus>> = {
  [OperationState.PENDING]: "pending",
  [OperationState.RUNNING]: "running",
  [OperationState.SUCCEEDED]: "succeeded",
  [OperationState.FAILED]: "failed",
  [OperationState.CANCELLED]: "cancelled",
};

/**
 * Where op stands. A server too old to send its state leaves it unspecified,
 * so it is read off done and the result as before — which cannot tell a
 * cancelled operation from a failed one, or a pending one from a running one.
 */
export function operationStatus(op: Operation): OpStatus {
  const s = FROM_STATE[op.state];
  if (s) return s;
  if (!op.done) return "running";
  return op.result.case === "error" ? "failed" : "succeeded";
}

/** Whether op may still be stopped. */
export const isActive = (s: OpStatus) => s === "pending" || s === "running";

/** A status's badge: its name and colour. */
export const OP_STATUS_BADGE = {
  pending: { label: "queued", variant: "secondary" },
  running: { label: "running", variant: "warning" },
  succeeded: { label: "done", variant: "success" },
  failed: { label: "failed", variant: "destructive" },
  cancelled: { label: "cancelled", variant: "outline" },
} as const satisfies Record<
  OpStatus,
  {
    label: string;
    variant: "secondary" | "warning" | "success" | "destructive" | "outline";
  }
>;

// The batch operation types, as the server names them (batchh.Op*).
const OP_BATCH_DELETE = "BatchDelete";
const OP_BATCH_COPY = "BatchCopy";
const OP_BATCH_UPDATE_TAGS = "BatchUpdateTags";
const OP_BATCH_RESTORE = "BatchRestoreObjects";

/** What op does, in words: the type, sharpened by what its request asked. */
export function operationLabel(op: Operation): string {
  const t = op.target;
  switch (op.type) {
    case OP_BATCH_DELETE:
      return t?.permanent ? "Delete permanently" : "Move to trash";
    case OP_BATCH_COPY:
      return "Copy objects";
    case OP_BATCH_UPDATE_TAGS:
      return t?.replace ? "Replace tags" : "Merge tags";
    case OP_BATCH_RESTORE:
      return "Restore from trash";
    default:
      // An unknown type, split at its capitals: "MigrateTenantStorageLayout"
      // reads "Migrate tenant storage layout".
      return (op.type || "Operation")
        .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
        .replace(/^./, (c) => c.toUpperCase())
        .replace(/ ([A-Z])/g, (_, c: string) => ` ${c.toLowerCase()}`);
  }
}

/** A collection named "tenants/{tenant}/collections/{name}", in parts. */
export function parseCollectionName(
  name: string | undefined,
): { tenant: string; collection: string } | null {
  const m = /^tenants\/([^/]+)\/collections\/(.+)$/.exec(name ?? "");
  return m ? { tenant: m[1], collection: m[2] } : null;
}

export interface OperationFailure {
  objectId: string;
  reason: string;
}

/** How a batch ended: its counts, and why each object that failed did. */
export interface OperationOutcome {
  total: number;
  succeeded: number;
  failed: number;
  alreadyApplied: number;
  failures: OperationFailure[];
}

function jsonFromAny(any: Any | undefined): Record<string, unknown> | null {
  if (!any) return null;
  const st = anyUnpackTo(any, StructSchema, create(StructSchema));
  if (!st) return null;
  const json = toJson(StructSchema, st);
  return json && typeof json === "object" && !Array.isArray(json)
    ? (json as Record<string, unknown>)
    : null;
}

const num = (v: unknown) => (typeof v === "number" ? v : 0);

/** The outcome a finished batch reported, or null for one that reported none. */
export function operationOutcome(op: Operation): OperationOutcome | null {
  if (op.result.case !== "response") return null;
  const r = jsonFromAny(op.result.value);
  if (!r || typeof r.total !== "number") return null;
  const failures = Array.isArray(r.failures)
    ? r.failures.flatMap((f) =>
        f && typeof f === "object"
          ? [
              {
                objectId: String(
                  (f as Record<string, unknown>).object_id ?? "",
                ),
                reason: String((f as Record<string, unknown>).reason ?? ""),
              },
            ]
          : [],
      )
    : [];
  return {
    total: r.total,
    succeeded: num(r.succeeded),
    failed: num(r.failed),
    alreadyApplied: num(r.already_applied),
    failures,
  };
}

/** How long op took, or has taken so far as of now. */
export function operationElapsedMs(op: Operation, now: Date): number | null {
  if (!op.createdAt) return null;
  const end = op.doneAt ? timestampDate(op.doneAt) : op.done ? null : now;
  if (!end) return null;
  return end.getTime() - timestampDate(op.createdAt).getTime();
}
