"use client";

// /tenants/<id>/buckets — placeholder for Phase 2.
//
// The proper page reuses the existing /buckets list component
// scoped to the current tenant — extracted as <BucketTable
// scopeTenantId={...}/> so the cross-tenant index at /buckets stays
// the source of truth for the rendering logic. Today: a temporary
// link out to the legacy flat URL so the tab is reachable and the
// breadcrumb / layout pattern can be validated end-to-end.

import Link from "next/link";

import { Card, CardContent } from "@/components/ui/Card";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "../tenant-context";

export default function TenantBucketsPage() {
  const tenant = useTenant();
  return (
    <Card>
      <CardContent className="space-y-2 p-6">
        <p className="text-sm font-medium">Buckets — under refactor</p>
        <p className={cn(T.helper, "max-w-prose")}>
          The tenant-scoped bucket list lands in Phase 2. Until then, the
          flat-URL list is still authoritative — filter manually by
          owner_tenant_id <span className={T.code}>{tenant.tenantId}</span>.
        </p>
        <p>
          <Link
            href="/buckets"
            className="text-sm font-medium text-primary hover:underline"
          >
            → Open /buckets (cross-tenant index)
          </Link>
        </p>
      </CardContent>
    </Card>
  );
}
