"use client";

import { TabStub } from "../_TabStub";

export default function TenantQuotasPage() {
  return (
    <TabStub
      title="Quotas"
      description={
        "Tenant-scoped storage / object / per-day quotas. The " +
        "QuotaService backend lives — the UI table lands in Phase 4."
      }
    />
  );
}
