"use client";

import { TabStub } from "../_TabStub";

export default function TenantBudgetPage() {
  return (
    <TabStub
      title="Budget"
      description={
        "Tenant-level spend limits + current period usage. Phase 4 " +
        "wires the budget editor here; today the global view is at " +
        "/tenant-budgets."
      }
      legacyHref="/tenant-budgets"
      legacyLabel="/tenant-budgets (cross-tenant)"
    />
  );
}
