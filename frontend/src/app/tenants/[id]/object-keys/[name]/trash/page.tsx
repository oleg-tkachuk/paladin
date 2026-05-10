"use client";

// Soft-deleted objects awaiting purge. Same wholesale-move
// deferral as Objects — links to the legacy /trash with the
// objectKey filter pre-applied.

import { useObjectKey } from "../objectkey-context";
import { OKTabStub } from "../_OKTabStub";

export default function ObjectKeyTrashPage() {
  const { objectKey: ok } = useObjectKey();
  return (
    <OKTabStub
      title="Trash"
      description={
        "Soft-deleted objects under this ObjectKey awaiting purge. " +
        "The legacy /trash page is the source of truth until Phase 5 " +
        "moves the table here; the link below pre-filters by this OK."
      }
      legacyHref={`/trash?objectKey=${encodeURIComponent(ok.objectKey)}`}
      legacyLabel="/trash (filtered to this ObjectKey)"
    />
  );
}
