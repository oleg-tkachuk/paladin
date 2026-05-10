"use client";

// TenantContext — exposes the resolved tenant from TenantLayout to
// every nested route. Avoids re-fetching the tenant in each tab page;
// Next's layout caches the layout component instance across tab
// switches, and this context propagates the in-memory tenant to
// children.
//
// API:
//   const { tenantId, slug, displayName } = useTenant();

import { createContext, useContext } from "react";

import type { ResolvedTenant } from "@/lib/resources/tenant-resolve";

const TenantCtx = createContext<ResolvedTenant | null>(null);

export function TenantProvider({
  value,
  children,
}: {
  value: ResolvedTenant;
  children: React.ReactNode;
}) {
  return <TenantCtx.Provider value={value}>{children}</TenantCtx.Provider>;
}

/**
 * Returns the resolved tenant. Throws when called outside a
 * TenantLayout — that's a programmer error worth crashing on rather
 * than rendering the page with `null` tenant fields. If a route
 * legitimately needs to render with-or-without tenant scope, use
 * `useTenantOptional()`.
 */
export function useTenant(): ResolvedTenant {
  const t = useContext(TenantCtx);
  if (!t) {
    throw new Error(
      "useTenant() called outside <TenantLayout>. " +
        "Wrap the route under app/tenants/[id]/.",
    );
  }
  return t;
}

export function useTenantOptional(): ResolvedTenant | null {
  return useContext(TenantCtx);
}
