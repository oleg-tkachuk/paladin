"use client";

import { BucketTabStub } from "../_BucketTabStub";

export default function BucketPolicyPage() {
  return (
    <BucketTabStub
      title="Policy"
      description={
        "Bucket-level Cedar overlay layered on top of the tenant " +
        "policy. The proto field exists today (Bucket.cedar_policy); " +
        "this editor lands once the tenant Policies tab ships its " +
        "Cedar-graph view so the two share an editor primitive."
      }
      legacyHref="/policies"
      legacyLabel="/policies (cross-tenant editor)"
    />
  );
}
