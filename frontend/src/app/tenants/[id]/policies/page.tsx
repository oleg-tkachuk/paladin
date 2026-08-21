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
        "The tenant-scoped Cedar policy graph (inherited + " +
        "per-bucket / per-collection overrides) lands in Phase 5. " +
        "Until then, the cross-tenant cedar templates editor at " +
        "/policies is the entry point — pick the template, attach " +
        "to the relevant entity from its detail page."
      }
      legacyHref="/policies"
      legacyLabel="/policies (system-wide)"
    />
  );
}
