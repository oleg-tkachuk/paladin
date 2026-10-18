// Why a collection would not delete, read off the server's answer: the
// objects that still hold it, live and trashed, and what to offer about them.

import { ErrorReason } from "@/gen/paladin/common/v1/error_reason_pb";
import {
  errorMessage,
  errorMetadata,
  errorReason,
} from "@/hooks/errorContract";
import { collectionTrashHref } from "@/lib/navigation/hrefs";

/** ERROR_REASON_COLLECTION_NOT_EMPTY's metadata keys (error_reason.proto). */
const HOLDING_KEY = {
  live: "live_objects",
  trashed: "trashed_objects",
} as const;

/** The objects that keep a collection from being deleted. */
export interface CollectionHolding {
  live: number;
  trashed: number;
}

const count = (v: string | undefined) =>
  v !== undefined && /^\d+$/.test(v) ? Number(v) : null;

/**
 * What holds the collection a delete was refused for; null for any other
 * failure, and for a refusal from a server that did not count.
 */
export function collectionHolding(err: unknown): CollectionHolding | null {
  if (errorReason(err) !== ErrorReason.COLLECTION_NOT_EMPTY) return null;
  const md = errorMetadata(err);
  const live = count(md[HOLDING_KEY.live]);
  const trashed = count(md[HOLDING_KEY.trashed]);
  return live === null || trashed === null ? null : { live, trashed };
}

/**
 * The notice a failed collection delete raises. The server's message says
 * what holds it; when that includes the trash, the notice opens it, since
 * nothing on the collection list shows a trashed object.
 */
export function collectionDeleteFailure(
  err: unknown,
  target: { tenantId: string; collection: string },
  open: (href: string) => void,
) {
  const holding = collectionHolding(err);
  return {
    type: "error" as const,
    title: "Deletion failed",
    message: errorMessage(err, "Delete failed"),
    action:
      holding && holding.trashed > 0
        ? {
            label: "Open its trash",
            onClick: () =>
              open(collectionTrashHref(target.tenantId, target.collection)),
          }
        : undefined,
  };
}
