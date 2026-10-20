"use client";

// /tenants/<id>/history — what changed in the tenant, as one timeline: its
// audit trail, as the server selects it (what its principals did and what was
// done to it), with the batches its collections ran. The raw log stays on the
// Audit tab.

import { HistoryTimeline } from "@/components/features/history/HistoryTimeline";
import { useResourceHistory } from "@/hooks/useResourceHistory";

import { useTenant } from "../tenant-context";

export default function TenantHistoryPage() {
  const tenant = useTenant();
  const history = useResourceHistory({
    kind: "tenant",
    tenantId: tenant.tenantId,
  });
  return (
    <HistoryTimeline
      history={history}
      what="this tenant"
      description="Changes made in this tenant and to it, and the batches its collections ran, newest first."
    />
  );
}
