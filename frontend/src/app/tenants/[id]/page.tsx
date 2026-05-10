"use client";

// Tenant Overview — landing page for /tenants/<id>. Summary tiles +
// quick-jump links to the tabs that already have real content.
// Placeholder until Phase 1+ fills out per-resource counts; the
// shape stays the same.

import Link from "next/link";
import {
  ArchiveBoxIcon,
  ArrowRightIcon,
  ShieldCheckIcon,
  TagIcon,
} from "@heroicons/react/24/outline";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { useTenant } from "./tenant-context";

const QUICK_LINKS: Array<{
  label: string;
  href: (slug: string) => string;
  description: string;
  icon: React.ElementType;
}> = [
  {
    label: "Buckets",
    href: (s) => `/tenants/${s}/buckets`,
    description: "S3 buckets owned by this tenant.",
    icon: ArchiveBoxIcon,
  },
  {
    label: "Object Keys",
    href: (s) => `/tenants/${s}/object-keys`,
    description: "Tenant-scoped namespaces routed to a bucket.",
    icon: TagIcon,
  },
  {
    label: "Policies",
    href: (s) => `/tenants/${s}/policies`,
    description: "Effective Cedar policy graph.",
    icon: ShieldCheckIcon,
  },
];

export default function TenantOverviewPage() {
  const tenant = useTenant();
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Identity</CardTitle>
        </CardHeader>
        <CardContent className={cn(T.helper, "space-y-1.5 text-xs")}>
          <div className="flex items-center gap-2">
            <span className="w-24 uppercase tracking-wider text-muted-foreground">
              slug
            </span>
            <span className={T.code}>{tenant.slug}</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="w-24 uppercase tracking-wider text-muted-foreground">
              tenant_id
            </span>
            <span className={T.code}>{tenant.tenantId}</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="w-24 uppercase tracking-wider text-muted-foreground">
              display
            </span>
            <span>{tenant.displayName}</span>
          </div>
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {QUICK_LINKS.map(({ label, href, description, icon: Icon }) => (
          <Link
            key={label}
            href={href(tenant.slug)}
            className="group rounded-xl focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <Card className="h-full transition-colors hover:border-primary/40 hover:bg-card/60">
              <CardHeader className="flex flex-row items-start gap-3 px-4">
                <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-secondary">
                  <Icon className="size-5 text-chart-2/85" />
                </div>
                <div className="min-w-0 flex-1 space-y-0.5">
                  <CardTitle className="text-sm">{label}</CardTitle>
                  <p className={cn(T.hint, "leading-snug")}>{description}</p>
                </div>
                <ArrowRightIcon className="size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-foreground" />
              </CardHeader>
            </Card>
          </Link>
        ))}
      </div>

      {/* Phase 1 minimal slice — counts + recent activity tiles
          land in a follow-up. Today the Overview is the
          identity card + the three quick-jump cards above. */}
    </div>
  );
}
