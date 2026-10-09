/**
 * An audit entry's action and error, split into what a reader scans for.
 *
 * Actions come in two shapes: an RPC path (/paladin.admin.v1.TenantService/
 * PurgeTenant) and a dotted event (iam.bootstrap_admin.reset). The method or
 * the event's last step is what happened; the plane and service say where.
 */

/** What an action does, by its verb; it colours the action's dot. */
export type ActionKind = "destroy" | "change" | "create" | "read" | "other";

export interface AuditAction {
  /** What happened: the RPC method, or the event's last step. */
  name: string;
  /** Where: "admin · TenantService", or the event's leading steps. */
  scope: string;
  kind: ActionKind;
}

// /paladin.<plane>.v<N>.<Service>/<Method>
const RPC_PATH = /^\/paladin\.([a-z]+)\.v\d+\.([A-Za-z0-9]+)\/([A-Za-z0-9]+)$/;
const EVENT_SEPARATOR = ".";
const SCOPE_SEPARATOR = " · ";
/** What an entry recorded with no action reads as. */
export const UNKNOWN_ACTION = "(unknown)";

// By the leading verb of a method, or the event's last step, lower-cased.
const KIND_BY_VERB: Record<string, ActionKind> = {
  delete: "destroy",
  purge: "destroy",
  revoke: "destroy",
  // Refresh-token reuse is a security event; it stands out like a removal.
  reuse: "destroy",
  update: "change",
  set: "change",
  clear: "change",
  rotate: "change",
  restore: "change",
  reset: "change",
  switch: "change",
  create: "create",
  issue: "create",
  login: "create",
  get: "read",
  list: "read",
  summarize: "read",
  inspect: "read",
};

// The first word of a CamelCase method, or of a snake_case step.
const LEADING_WORD = /^[A-Z]?[a-z]+/;

function kindOf(name: string): ActionKind {
  const verb = name.match(LEADING_WORD)?.[0].toLowerCase() ?? "";
  return KIND_BY_VERB[verb] ?? "other";
}

export function parseAuditAction(action: string): AuditAction {
  if (!action) return { name: UNKNOWN_ACTION, scope: "", kind: "other" };
  const rpc = RPC_PATH.exec(action);
  if (rpc) {
    const [, plane, service, method] = rpc;
    return {
      name: method,
      scope: `${plane}${SCOPE_SEPARATOR}${service}`,
      kind: kindOf(method),
    };
  }
  const steps = action.split(EVENT_SEPARATOR);
  const name = steps.pop() ?? action;
  return {
    name,
    scope: steps.join(EVENT_SEPARATOR),
    kind: kindOf(name),
  };
}

export interface AuditError {
  /** The Connect code the call failed with, when the message names one. */
  code?: string;
  message: string;
}

// The audit log records a failure as "<connect code>: <message>".
const CONNECT_CODES = new Set([
  "canceled",
  "unknown",
  "invalid_argument",
  "deadline_exceeded",
  "not_found",
  "already_exists",
  "permission_denied",
  "resource_exhausted",
  "failed_precondition",
  "aborted",
  "out_of_range",
  "unimplemented",
  "internal",
  "unavailable",
  "data_loss",
  "unauthenticated",
]);
const CODE_PREFIX = /^([a-z_]+): ([\s\S]*)$/;

export function parseAuditError(errorMessage: string): AuditError {
  const m = CODE_PREFIX.exec(errorMessage);
  if (m && CONNECT_CODES.has(m[1])) return { code: m[1], message: m[2] };
  return { message: errorMessage };
}
