"use client";

// Tenant subtree layout — applies to every route under
// /tenants/<id>/. Provides:
//
//   1. Tenant resolution (slug-or-UUID → canonical) with a single
//      RPC at mount; child routes read the resolved tenant from
//      the TenantContext below.
//   2. Sticky page header with tenant display name + tabs.
//   3. Loading / not-found shells.
//
// Why a layout (not just a wrapper component on each page): Next's
// app-router preserves layouts across navigations, so swapping
// between the Buckets and Object Keys tabs DOESN'T re-fetch the
// tenant — the layout stays mounted. Each tab page renders its
// own content area underneath.

import { use } from "react";
import { notFound } from "next/navigation";

import { PageHeader } from "@/components/layout/PageHeader";
import { TenantTabs } from "@/components/layout/TenantTabs";
import { Skeleton } from "@/components/ui/Skeleton";
import { useTenantResolve } from "@/lib/resources/tenant-resolve";

import { TenantProvider } from "./tenant-context";

export default function TenantLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  // Next.js 15+: params is a Promise in app-router layouts. `use(...)`
  // unwraps in client components without converting the layout to
  // an async server component (which would lose the "use client"
  // hooks below).
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const { tenant, loading, error } = useTenantResolve(id);

  if (loading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-12 w-72" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  if (error || !tenant) {
    // The resolver returns ErrNotFound when the slug/UUID doesn't
    // match any visible tenant; surface as 404 rather than a generic
    // "load failed" toast — wrong URL is the dominant case.
    notFound();
  }

  return (
    <TenantProvider value={tenant}>
      <div className="space-y-4">
        <PageHeader
          title={tenant.displayName}
          description={`Tenant scope — slug: ${tenant.slug}`}
          showDefaultActions={false}
        />
        {/* Tab strip uses slug-form URLs; if the page landed via a
            UUID, the resolver has already canonicalised the address
            bar by the time we render. */}
        <TenantTabs tenantId={tenant.slug} />
        <div>{children}</div>
      </div>
    </TenantProvider>
  );
}
