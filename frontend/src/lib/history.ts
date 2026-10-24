// A resource's history, after Argo CD's deployment history: each change that
// reached it, who made it — a person, a token acting for one, or Paladin
// itself — what it was in plain words, when, how long it ran and how it
// ended. Read from two records: the audit log, which holds every change an
// admin RPC made, and the operations, which hold the long-running batches
// and how far each got. Pure, so the timeline and its tests agree.

import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";

import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";
import {
  parseAuditAction,
  parseAuditError,
} from "@/components/features/audit/action";
import { SUBJECT_PREFIX } from "@/lib/actor";
import { isUuid } from "@/lib/resources/tenant-resolve";
import {
  OP_STATUS_BADGE,
  OP_STATUS_TONE,
  camelWords,
  operationElapsedMs,
  operationError,
  operationLabel,
  operationStatus,
} from "@/lib/operations";
import { StatusTone } from "@/lib/ui/status";

/**
 * Who made a change: a person signed in; a token — an API key, a capability,
 * an OAuth client — which is how automation acts; or Paladin itself.
 */
export type ActorKind = "user" | "token" | "system";

/** What each kind of actor is called beside its name. */
export const ACTOR_KIND_LABEL: Record<ActorKind, string> = {
  user: "User",
  token: "Automated",
  system: "System",
};

/**
 * The kind of actor a subject names. A person's subject is their user id; a
 * token's carries its kind ("apikey:…", "capability:…"); an entry with none
 * was made by Paladin.
 */
export function actorKind(subject: string): ActorKind {
  if (!subject || subject.startsWith(SUBJECT_PREFIX.system)) return "system";
  return isUuid(subject) ? "user" : "token";
}

/** An audit action in words: "/paladin.admin.v1.BucketService/UpdateBucket" reads "Update bucket". */
export function plainAction(action: string): string {
  const { name } = parseAuditAction(action);
  // An event's last step is snake_case: "bootstrap_admin.reset" ends "reset".
  return camelWords(name.replace(/_(\w)/g, (_, c: string) => c.toUpperCase()));
}

export interface HistoryEntry {
  /** Unique across both records. */
  key: string;
  at: Date | null;
  tone: StatusTone;
  /** How it ended, in a word: "Succeeded", "Failed", "Running". */
  outcome: string;
  /** What was done, in plain words. */
  action: string;
  actor: { subject: string; tenantId: string; kind: ActorKind };
  /** How long it ran, when the record says; null when it does not. */
  durationMs: number | null;
  /** Why it failed, in the server's words; "" when it did not. */
  error: string;
  /** The full record. */
  href: string;
  /** Which record it came from. */
  source: "audit" | "operation";
}

const date = (ts: Timestamp | undefined) => (ts ? timestampDate(ts) : null);

/** An audit entry as a history entry. An admin RPC is over when it is logged. */
export function historyFromAudit(e: AuditLogEntry): HistoryEntry {
  const failed = e.errorMessage !== "";
  return {
    key: `audit:${e.entryId}`,
    at: date(e.at),
    tone: failed ? StatusTone.Danger : StatusTone.Success,
    outcome: failed ? "Failed" : "Succeeded",
    action: plainAction(e.action),
    actor: {
      subject: e.actorSubject,
      tenantId: e.actorTenantId,
      kind: actorKind(e.actorSubject),
    },
    durationMs: null,
    error: failed ? parseAuditError(e.errorMessage).message : "",
    href: `/audit?id=${encodeURIComponent(e.entryId)}`,
    source: "audit",
  };
}

/**
 * An operation as a history entry: running is a change under way, and its
 * duration so far is measured against `now`.
 */
export function historyFromOperation(op: Operation, now: Date): HistoryEntry {
  const status = operationStatus(op);
  const label = OP_STATUS_BADGE[status].label;
  return {
    key: `operation:${op.name}`,
    at: date(op.createdAt),
    tone: OP_STATUS_TONE[status],
    outcome: label.charAt(0).toUpperCase() + label.slice(1),
    action: operationLabel(op),
    actor: {
      subject: op.initiatorSubject,
      tenantId: op.initiatorTenantId,
      kind: actorKind(op.initiatorSubject),
    },
    durationMs: operationElapsedMs(op, now),
    error: operationError(op),
    href: `/operations?id=${encodeURIComponent(op.name)}`,
    source: "operation",
  };
}

/** Both records as one timeline, newest first; an undated entry goes last. */
export function mergeHistory(
  ...lists: readonly HistoryEntry[][]
): HistoryEntry[] {
  const time = (e: HistoryEntry) => e.at?.getTime() ?? -Infinity;
  return lists.flat().sort((a, b) => time(b) - time(a));
}

/**
 * The audit filter for what was done to one resource and, with `nested`, to
 * what it holds: a bucket's grants and quota are named under the bucket. The
 * slash keeps bucket "logs" from matching "logs-old". A backend's history
 * leaves its buckets out: each bucket has a history of its own.
 */
/**
 * The operations filter for one tenant's, as the server's CEL names it. The
 * request names the tenant too; the filter keeps a server that predates
 * tenant_id — mid-rollout, behind a newer console — from passing the
 * caller's own tenant's operations off as this one's.
 */
export function operationsFilterFor(tenantId: string): string {
  return `tenant_id == ${JSON.stringify(tenantId)}`;
}

export function auditFilterFor(resourceName: string, nested = true): string {
  const own = `resource_name == ${JSON.stringify(resourceName)}`;
  if (!nested) return own;
  const under = JSON.stringify(`${resourceName}/`);
  return `${own} || resource_name.startsWith(${under})`;
}
