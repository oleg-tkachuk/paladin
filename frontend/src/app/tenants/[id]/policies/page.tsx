"use client";

import { TabStub } from "../_TabStub";

// /tenants/<id>/policies — effective Cedar policy graph for THIS
// tenant (vs. /policies which stays as the system-wide cedar
// templates editor + simulator).
export default function TenantPoliciesPage() {
  return (
    <TabStub
      title="Effective Cedar policy"
      description={
        "This tenant's effective Cedar policy — inherited, with its " +
        "per-bucket and per-collection overrides — is not shown here " +
        "yet. Edit, validate and simulate policies for it at " +
        "/policies: pick the tenant as the target."
      }
      legacyHref="/policies"
      legacyLabel="/policies (system-wide)"
    />
  );
}
