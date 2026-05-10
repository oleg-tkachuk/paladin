"use client";

import { TabStub } from "../_TabStub";

export default function TenantAuditLogPage() {
  return (
    <TabStub
      title="Audit log"
      description={
        "Append-only mutation log filtered to this tenant. The " +
        "cross-tenant view at /audit (platform-admin only) is the " +
        "source of truth until Phase 4 wires the tenant-scoped " +
        "filter here."
      }
      legacyHref="/audit"
      legacyLabel="/audit (cross-tenant)"
    />
  );
}
