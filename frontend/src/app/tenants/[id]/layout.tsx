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
// between the Buckets and Collections tabs DOESN'T re-fetch the
// tenant — the layout stays mounted. Each tab page renders its
// own content area underneath.

import Link from "next/link";
import { notFound, useParams } from "next/navigation";
import {
  ArrowPathIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { TenantTabs } from "@/components/layout/TenantTabs";
import { Skeleton } from "@/components/ui/Skeleton";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/Card";
import { useTenantResolve } from "@/lib/resources/tenant-resolve";

import { TenantProvider } from "./tenant-context";

export default function TenantLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // useParams() (sync, from the router context) instead of `use(params)`
  // (Promise unwrap). Next.js's app router passes a NEW Promise for
  // `params` to the layout on every sub-route change — even when the
  // dynamic segment value is identical — and `use(newPromise)`
  // suspends, causing the layout to flash its loading skeleton on
  // every tab navigation. useParams reads the resolved value off the
  // router context synchronously, so this layout stays mounted and
  // useTenantResolve's cache survives.
  const params = useParams<{ id: string }>();
  const id = params?.id ?? "";
  const { tenant, loading, error, errorIsNotFound, retry } =
    useTenantResolve(id);

  if (loading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-12 w-72" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  // Real 404 (slug/UUID doesn't match any visible tenant) → surface
  // the standard 404 chrome. Wrong URL is the failure mode this
  // catches.
  if (errorIsNotFound) {
    notFound();
  }

  // Transient error (network blip, 401 mid-refresh, server hiccup):
  // render an actionable Retry card instead of nuking the route to
  // notFound(). Without this, an access-token expiry that races
  // with the GetTenant call manifested as a "blank page on refresh"
  // — refreshing again fixed it because the second load picked up
  // a fresh token, but the first load was already 404'd.
  if (error || !tenant) {
    return (
      <Card className="flex flex-col items-center gap-3 p-10 text-center">
        <ExclamationTriangleIcon className="size-10 text-destructive opacity-70" />
        <div>
          <p className="text-sm font-medium">Failed to load tenant</p>
          <p className="mt-1 max-w-prose text-xs text-muted-foreground">
            {error?.message || "Unknown error"}
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={retry}>
          <ArrowPathIcon className="size-4" />
          Retry
        </Button>
      </Card>
    );
  }

  return (
    <TenantProvider value={tenant}>
      <div className="space-y-4">
        <PageHeader
          title={tenant.displayName}
          description={`Tenant scope — slug: ${tenant.slug}`}
          showDefaultActions={false}
        />
        {tenant.trashed && (
          <div
            role="status"
            className="space-y-1 rounded-md border border-warning/40 bg-warning/10 px-4 py-3 text-sm"
          >
            <p className="font-medium text-warning">
              This tenant is in the trash.
            </p>
            <p className="text-muted-foreground">
              It is frozen until it is restored or purged: nothing in it
              changes, its scheduled work and event deliveries wait, and its
              credentials do not work. You can still read and download its data,
              and revoke its access.{" "}
              <Link href="/trash" className="text-primary underline">
                Restore or purge it from the Trash
              </Link>
              .
            </p>
          </div>
        )}
        {/* Tab strip uses slug-form URLs; if the page landed via a
            UUID, the resolver has already canonicalised the address
            bar by the time we render. */}
        <TenantTabs tenantId={tenant.slug} />
        <div>{children}</div>
      </div>
    </TenantProvider>
  );
}
