// Starting a batch again for the objects it could not act on. Pure, so what a
// retry asks for is tested once for every place that offers one.
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";

import {
  OP_BATCH_COPY,
  OP_BATCH_DELETE,
  OP_BATCH_RESTORE,
  OP_BATCH_UPDATE_TAGS,
  operationOutcome,
} from "@/lib/operations";

/** What separates an object's id from its collection in its resource name. */
const OBJECTS_SEGMENT = "/objects/";

/**
 * The batch to start again for op's failures: the same kind over the same
 * collection, of the objects it could not act on, asking for what it asked —
 * a copy's destination and key prefix, a tag update's tags. null for an
 * operation with none, and for a tag update whose target names no tags: from
 * a server that does not send them, a retry would ask for none, and with
 * replace that clears every tag.
 */
export type RetryRequest =
  | { kind: "delete"; parent: string; names: string[]; permanent: boolean }
  | { kind: "restore"; parent: string; names: string[] }
  | {
      kind: "copy";
      parent: string;
      names: string[];
      destination: string;
      keyPrefix: string;
    }
  | {
      kind: "tags";
      parent: string;
      names: string[];
      tags: Record<string, string>;
      replace: boolean;
    };

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
    case OP_BATCH_COPY: {
      const destination = op.target?.destination ?? "";
      if (!destination) return null;
      return {
        kind: "copy",
        parent,
        names,
        destination,
        keyPrefix: op.target?.destinationKeyPrefix ?? "",
      };
    }
    case OP_BATCH_UPDATE_TAGS: {
      const tags = op.target?.tags ?? {};
      if (Object.keys(tags).length === 0) return null;
      return {
        kind: "tags",
        parent,
        names,
        tags,
        replace: op.target?.replace ?? false,
      };
    }
    default:
      return null;
  }
}
