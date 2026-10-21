// What the console says about a long-running operation: where it stands, what
// it was asked to do, and how it ended. One reading of the Operation message,
// shared by every place that shows one.

import { toJson } from "@bufbuild/protobuf";
import { anyUnpackTo, StructSchema, type Any } from "@bufbuild/protobuf/wkt";
import { create } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";

import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import {
  progressFromFailure,
  progressFromMetadata,
  type OpProgress,
} from "@/lib/operationProgress";
import { OperationState } from "@/gen/paladin/common/v1/operation_pb";
import { StatusTone } from "@/lib/ui/status";

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
/** The collection an operation's resource name sits in: operations/{id}. */
export const OPERATIONS_COLLECTION = "operations/";

/**
 * An operation's id, its resource name without the collection: what a list
 * row has room for, the full name being on its record.
 */
export function operationId(op: Pick<Operation, "name">): string {
  return op.name.startsWith(OPERATIONS_COLLECTION)
    ? op.name.slice(OPERATIONS_COLLECTION.length)
    : op.name;
}

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
      return camelWords(op.type || "Operation");
  }
}

/**
 * A CamelCase name in words, split at its capitals and in sentence case:
 * "MigrateTenantStorageLayout" reads "Migrate tenant storage layout".
 */
export function camelWords(name: string): string {
  return name
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/^./, (c) => c.toUpperCase())
    .replace(/ ([A-Z])/g, (_, c: string) => ` ${c.toLowerCase()}`);
}

/**
 * Each state in the console's status vocabulary: queued and running are a
 * change under way, a cancelled operation a stop someone chose.
 */
export const OP_STATUS_TONE: Record<OpStatus, StatusTone> = {
  pending: StatusTone.Progress,
  running: StatusTone.Progress,
  succeeded: StatusTone.Success,
  failed: StatusTone.Danger,
  cancelled: StatusTone.Suspended,
};

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

/** Why op failed, in its own words; "" for one that did not. */
export function operationError(op: Operation): string {
  if (op.result.case === "error") return op.result.value.message || "error";
  return "";
}

/**
 * How far the work got — live for a running operation, last-known for one
 * whose worker died. A failure with no snapshot is null: the console says
 * nothing rather than guessing zero.
 */
export function operationProgress(op: Operation): OpProgress | null {
  if (op.result.case === "error") {
    return progressFromFailure(op.result.value.details);
  }
  return progressFromMetadata(op.metadata);
}

/** "1 object", "3 objects". */
export const objectCountLabel = (n: number) =>
  `${n} object${n === 1 ? "" : "s"}`;

/** One line of how a finished batch went, or null for one that said not. */
export function outcomeLine(op: Operation): string | null {
  const o = operationOutcome(op);
  if (!o) return null;
  const parts = [`${o.succeeded} of ${o.total} done`];
  if (o.alreadyApplied > 0) parts.push(`${o.alreadyApplied} already so`);
  if (o.failed > 0) parts.push(`${o.failed} failed`);
  return parts.join(" · ");
}

/** How far along op's progress is, 0–100. */
export function progressPercent(p: OpProgress): number {
  if (p.total <= 0) return 0;
  return Math.min(100, (p.processed / p.total) * 100);
}

/** Which operations a list shows, by where they stand. */
export type OpView = "all" | "active" | "failed" | "done";
/** The view the operations list opens on. */
export const DEFAULT_OP_VIEW: OpView = "all";
export const OP_VIEWS: readonly { value: OpView; label: string }[] = [
  { value: "all", label: "All" },
  { value: "active", label: "Active" },
  { value: "failed", label: "Failed" },
  { value: "done", label: "Done" },
];
/** Whether a view keeps an operation in this state. */
export const OP_VIEW_KEEPS: Record<OpView, (s: OpStatus) => boolean> = {
  all: () => true,
  active: isActive,
  failed: (s) => s === "failed",
  done: (s) => s === "succeeded" || s === "cancelled",
};

// The states as the server's filter names them (filter/cel OperationState*).
const CEL_STATE: Record<OpStatus, string> = {
  pending: "PENDING",
  running: "RUNNING",
  succeeded: "SUCCEEDED",
  failed: "FAILED",
  cancelled: "CANCELLED",
};

/**
 * The server's CEL filter for a view, "" for all. Read off OP_VIEW_KEEPS, so
 * the list the server returns and the one a client narrows cannot disagree;
 * `state in [...]` is pushed into the query, so every page is full.
 */
export function operationsFilter(view: OpView): string {
  const states = (Object.keys(CEL_STATE) as OpStatus[]).filter(
    OP_VIEW_KEEPS[view],
  );
  if (states.length === Object.keys(CEL_STATE).length) return "";
  return `state in [${states.map((s) => JSON.stringify(CEL_STATE[s])).join(", ")}]`;
}

/**
 * Whether op matches a search, by what the page shows of it: its label, its
 * type, its name, the collections it acts on and why it failed.
 */
export function operationMatches(op: Operation, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [
    operationLabel(op),
    op.type,
    op.name,
    op.target?.collection ?? "",
    op.target?.destination ?? "",
    operationError(op),
  ].some((s) => s.toLowerCase().includes(q));
}
