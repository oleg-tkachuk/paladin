// Starting a batch again for the objects it could not act on. Pure, so what a
// retry asks for is tested once for every place that offers one.
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";

import {
  OP_BATCH_DELETE,
  OP_BATCH_RESTORE,
  operationOutcome,
} from "@/lib/operations";

/** What separates an object's id from its collection in its resource name. */
const OBJECTS_SEGMENT = "/objects/";

/**
 * The batch to start again for op's failures: the same kind over the same
 * collection, of the objects it could not act on. null for an operation with
 * none, and for a copy or a tag update, whose key prefix and tags the
 * operation does not carry, so a retry could not ask for what it asked.
 */
export type RetryRequest =
  | { kind: "delete"; parent: string; names: string[]; permanent: boolean }
  | { kind: "restore"; parent: string; names: string[] };

export function retryRequest(op: Operation): RetryRequest | null {
  const parent = op.target?.collection ?? "";
  const ids = operationOutcome(op)?.failures.map((f) => f.objectId) ?? [];
  if (!parent || ids.length === 0) return null;
  const names = ids.filter(Boolean).map((id) => parent + OBJECTS_SEGMENT + id);
  switch (op.type) {
    case OP_BATCH_DELETE:
      return {
        kind: "delete",
        parent,
        names,
        permanent: op.target?.permanent ?? false,
      };
    case OP_BATCH_RESTORE:
      return { kind: "restore", parent, names };
    default:
      return null;
  }
}
