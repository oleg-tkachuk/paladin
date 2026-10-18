// The fields ListAuditLog sorts by, as its order_by names them. The server
// refuses any other (backend admindomain.AuditOrderFields).
export const AUDIT_ORDER = {
  at: "at",
  action: "action",
  actor: "actor_subject",
  resource: "resource_name",
} as const;

export type AuditOrderField = (typeof AUDIT_ORDER)[keyof typeof AUDIT_ORDER];
