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
import type { FacetCount } from "@/gen/paladin/common/v1/facet_pb";
import {
  OperationState,
  OperationStateSchema,
} from "@/gen/paladin/common/v1/operation_pb";
import {
  groupFacetCounts,
  type Facet,
  type FacetCounts,
  type FacetOption,
  type FacetSelection,
} from "@/lib/table/facets";
import { StatusTone } from "@/lib/ui/status";

/**
 * Where an operation stands. partial is a batch that ran to its end, so the
 * server calls it succeeded, but left objects it could not act on.
 */
export type OpStatus =
  "pending" | "running" | "succeeded" | "partial" | "failed" | "cancelled";

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
  const s = FROM_STATE[op.state] ?? fromDone(op);
  return s === "succeeded" && op.failedCount > 0n ? "partial" : s;
}

function fromDone(op: Operation): OpStatus {
  if (!op.done) return "running";
  return op.result.case === "error" ? "failed" : "succeeded";
}

/** Whether s left something undone: it failed, or left objects behind. */
export const hasFailures = (s: OpStatus) => s === "failed" || s === "partial";

/** Whether op may still be stopped. */
export const isActive = (s: OpStatus) => s === "pending" || s === "running";

/** A status's badge: its name and colour. */
export const OP_STATUS_BADGE = {
  pending: { label: "queued", variant: "secondary" },
  running: { label: "running", variant: "warning" },
  succeeded: { label: "done", variant: "success" },
  partial: { label: "partly failed", variant: "warning" },
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
export const OP_BATCH_DELETE = "BatchDelete";
export const OP_BATCH_COPY = "BatchCopy";
export const OP_BATCH_UPDATE_TAGS = "BatchUpdateTags";
export const OP_BATCH_RESTORE = "BatchRestoreObjects";

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
  partial: StatusTone.Warning,
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
  failed: hasFailures,
  done: (s) => s === "succeeded" || s === "cancelled",
};

/**
 * The server's CEL filter for each view, "" for all: what OP_VIEW_KEEPS keeps,
 * in the terms the filter has (filter/cel OperationSchema). A partly failed
 * batch is SUCCEEDED there with has_failures, and a failed operation has
 * failures too, so `has_failures` is the failed view whole. Each is a
 * conjunction the server pushes into its query, so every page is full.
 */
const OP_VIEW_FILTER: Record<OpView, string> = {
  all: "",
  active: 'state in ["PENDING", "RUNNING"]',
  failed: "has_failures",
  done: 'state in ["SUCCEEDED", "CANCELLED"] && !has_failures',
};

/** The server's CEL filter for a view, "" for all. */
export function operationsFilter(view: OpView): string {
  return OP_VIEW_FILTER[view];
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

// ─── facets ────────────────────────────────────────────────────────────────

// The facets the server narrows and counts an operation listing by, as it
// names them (operationh.FacetState, operationh.FacetType).
export const OP_FACET_STATE = "state";
export const OP_FACET_TYPE = "type";

/** A state's value as the facet names it: the enum's own name, "FAILED". */
function stateValue(state: OperationState): string {
  return OperationStateSchema.value[state]?.localName ?? "";
}

/**
 * Each state the state facet offers, in the order an operation moves, toned
 * as its status is. Succeeded holds the partly failed too: the server counts
 * by state, which has no partial.
 */
const OP_STATE_OPTIONS: readonly FacetOption[] = (
  [
    [OperationState.PENDING, "Queued"],
    [OperationState.RUNNING, "Running"],
    [OperationState.SUCCEEDED, "Succeeded"],
    [OperationState.FAILED, "Failed"],
    [OperationState.CANCELLED, "Cancelled"],
  ] as const
).map(([state, label]) => ({
  value: stateValue(state),
  label,
  tone: OP_STATUS_TONE[FROM_STATE[state] ?? "pending"],
}));

/** The operations page's facets: where an operation stands, what it does. */
export const OPERATION_FACETS: readonly Facet<Operation>[] = [
  {
    id: OP_FACET_STATE,
    label: "State",
    values: (op) => [stateValue(op.state)],
    options: OP_STATE_OPTIONS,
  },
  {
    id: OP_FACET_TYPE,
    label: "Type",
    values: (op) => [op.type],
    optionLabel: camelWords,
  },
];

/**
 * The server's counts as the facets name their answers: a state comes by
 * its wire name, "OPERATION_STATE_FAILED", and is offered as "FAILED".
 */
export function operationFacetCounts(
  counts: readonly FacetCount[],
): FacetCounts {
  return groupFacetCounts(counts, (c) => {
    if (c.facet !== OP_FACET_STATE) return c.value;
    return (
      OperationStateSchema.values.find((v) => v.name === c.value)?.localName ??
      c.value
    );
  });
}

/** The facet choices as the request names them. */
export function operationFacetsRequest(selection: FacetSelection): {
  states: OperationState[];
  types: string[];
} {
  const states = (selection[OP_FACET_STATE] ?? []).flatMap((value) => {
    const v = OperationStateSchema.values.find((e) => e.localName === value);
    return v ? [v.number as OperationState] : [];
  });
  return { states, types: [...(selection[OP_FACET_TYPE] ?? [])] };
}
