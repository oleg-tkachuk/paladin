"use client";

// ObjectKeys routed to this bucket. Implementation deferred to
// Phase 3 — the cross-tenant /object-keys page already filters by
// backend/bucket, so the immediate value of this tab is letting an
// operator pivot from "I'm looking at this bucket" → "what writes
// here?" without re-typing the filter.

import { BucketTabStub } from "../_BucketTabStub";

export default function BucketObjectKeysPage() {
  return (
    <BucketTabStub
      title="Object Keys"
      description={
        "List of ObjectKeys whose route binds to this bucket. Lands " +
        "in Phase 3 alongside the tenant-scoped Object Keys tab; " +
        "until then, the cross-tenant page accepts a backend+bucket " +
        "filter."
      }
      legacyHref="/object-keys"
      legacyLabel="/object-keys (cross-tenant)"
    />
  );
}
