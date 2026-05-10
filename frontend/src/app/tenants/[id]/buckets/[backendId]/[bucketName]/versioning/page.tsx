"use client";

import { BucketTabStub } from "../_BucketTabStub";

export default function BucketVersioningPage() {
  return (
    <BucketTabStub
      title="Versioning"
      description={
        "Object-versioning toggle plus retention window. The backend " +
        "currently exposes versioning state on the underlying S3 API " +
        "only; once the BucketService surfaces it, this tab wires the " +
        "switch + retention editor."
      }
    />
  );
}
