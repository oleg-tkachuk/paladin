// The fields ListObjects sorts by, as its order_by names them. The server
// refuses any other (backend objecth.ObjectOrderFields), so this list and
// that one must agree; a test of the RPC contract holds them together.
export const OBJECT_ORDER = {
  key: "key",
  contentType: "content_type",
  size: "size_bytes",
  state: "state",
  createdAt: "created_at",
  terminatedAt: "terminated_at",
} as const;

export type ObjectOrderField = (typeof OBJECT_ORDER)[keyof typeof OBJECT_ORDER];

const FIELDS: readonly string[] = Object.values(OBJECT_ORDER);

/** Whether a sort read from somewhere untrusted — a URL — is one to send. */
export function isObjectOrderField(v: string): v is ObjectOrderField {
  return FIELDS.includes(v);
}
