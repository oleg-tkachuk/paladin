"use client";

import { TabStub } from "../_TabStub";

export default function TenantCapabilitiesPage() {
  return (
    <TabStub
      title="Capabilities"
      description={
        "Capabilities issued under this tenant. Phase 4 wires the " +
        "capabilities table here; the cross-tenant view stays at " +
        "/capabilities for platform-admin oversight."
      }
      legacyHref="/capabilities"
      legacyLabel="/capabilities (cross-tenant)"
    />
  );
}
