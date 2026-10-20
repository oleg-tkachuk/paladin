"use client";

// A tenant's billing tab: what its capabilities spent over a period, against
// its budget, in the tenant's own workspace, so the trail reads
// Tenants › tenant › Billing. The period is in the URL, so the link from
// every tenant's spend (/billing) opens on the same one.

import { Suspense } from "react";

import { TenantBilling } from "@/components/features/billing/TenantBilling";
import { useBillingPeriod } from "@/components/features/billing/useBillingPeriod";

import { useTenant } from "../tenant-context";

export default function TenantBillingPage() {
  // useSearchParams, under the period, requires a Suspense boundary.
  return (
    <Suspense>
      <TenantBillingTab />
    </Suspense>
  );
}

function TenantBillingTab() {
  const { tenantId, slug, displayName } = useTenant();
  const state = useBillingPeriod();
  return (
    <TenantBilling
      tenantId={tenantId}
      tenantHandle={slug}
      tenantName={displayName || slug}
      state={state}
    />
  );
}
