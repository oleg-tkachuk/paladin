"use client";

// Live objects under this ObjectKey. Wholesale move of the
// /objects page (heavy: 1k+ LOC, multipart upload state, pagination,
// filters) is tracked for Phase 5 cleanup. For this slice the tab
// links to the legacy view with the objectKey filter pre-applied,
// so operators can pivot from "I'm in this OK" → "show its
// objects" with one click and zero re-typing.

import { useObjectKey } from "../objectkey-context";
import { OKTabStub } from "../_OKTabStub";

export default function ObjectKeyObjectsPage() {
  const { objectKey: ok } = useObjectKey();
  return (
    <OKTabStub
      title="Objects"
      description={
        "Live objects routed to this ObjectKey. Wholesale move of " +
        "the cross-tenant Objects page (multipart upload state, " +
        "filters, pagination) lands in Phase 5; until then the legacy " +
        "view is the source of truth, pre-filtered to this OK."
      }
      legacyHref={`/objects?objectKey=${encodeURIComponent(ok.objectKey)}`}
      legacyLabel="/objects (filtered to this ObjectKey)"
    />
  );
}
