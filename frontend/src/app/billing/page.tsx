"use client";

// /billing — every tenant's capability spend over a period, from the charges
// ledger, the most first: the platform admin's view. One tenant's billing is
// that tenant's tab (/tenants/{t}/billing), so its trail reads
// Tenants › tenant › Billing; a row here leads there with the same period.
//
// Anyone else is the admin of their own tenant at most, and is taken to its
// tab; so is an old /billing?tenant= link.

import { Suspense, useEffect } from "react";
import { useRouter, useSearchParams } from "next/navigation";

import { PageHeader } from "@/components/layout/PageHeader";
import { Card } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import { PlatformBilling } from "@/components/features/billing/PlatformBilling";
import { useBillingPeriod } from "@/components/features/billing/useBillingPeriod";
import { isPlatformAdmin } from "@/constants/roles";
import { useAuth } from "@/context/AuthContext";
import { useScope } from "@/context/ScopeContext";
import { tenantBillingHref } from "@/lib/navigation/hrefs";
import { T } from "@/lib/ui/typography";

/** The parameter old links named one tenant by. */
const TENANT_PARAM = "tenant";

export default function BillingPage() {
  // useSearchParams requires a Suspense boundary under the Next.js app router.
  return (
    <Suspense>
      <BillingView />
    </Suspense>
  );
}

function BillingView() {
  const router = useRouter();
  const search = useSearchParams();
  const { user } = useAuth();
  const { tenantId: scopeTenantId } = useScope();
  const state = useBillingPeriod();
  const crossTenant = isPlatformAdmin(user?.roles ?? []);
  const named = search.get(TENANT_PARAM);
  // Where this page sends its visitor instead: the tenant an old link names,
  // or, for anyone who cannot read every tenant, their own.
  const target = named || (crossTenant ? null : scopeTenantId || null);
  useEffect(() => {
    if (target) router.replace(`${tenantBillingHref(target)}${state.query}`);
  }, [router, target, state.query]);

  if (target) return <Skeleton className="h-64 w-full" />;
  if (!crossTenant) {
    return (
      <Card className="space-y-2 p-8 text-center">
        <h3 className={T.cardTitleProse}>No tenant to show</h3>
        <p className={T.helper}>
          Billing is read per tenant; choose one in the scope switcher.
        </p>
      </Card>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Billing"
        description="What the platform was charged over the period, how it moved, and which tenants it came from."
        showDefaultActions={false}
      />
      <PlatformBilling
        state={state}
        hrefOf={(tenant) => `${tenantBillingHref(tenant)}${state.query}`}
      />
    </div>
  );
}
