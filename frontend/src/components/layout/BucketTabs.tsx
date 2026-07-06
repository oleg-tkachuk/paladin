"use client";

// BucketTabs — second-level sub-nav rendered inside the bucket
// detail layout (`/tenants/<id>/buckets/<backend>/<name>/...`).
//
// Tabs reflect the surfaces the backend exposes per-bucket today
// plus near-term placeholders so the URL shape doesn't churn when
// they ship:
//   1. Overview      — metadata, provision state, summary counts.
//   2. Lifecycle     — transition / expiration rules (already wired).
//   3. Policy        — bucket-level Cedar overlay.
//   4. Replication   — cross-region / cross-backend mirror config.
//   5. Versioning    — object-versioning toggle + retention.
//   6. Object Lock   — WORM default-retention (governance/compliance).
//   7. Object Keys   — ObjectKeys routed to this bucket.

import Link from "next/link";
import { usePathname } from "next/navigation";

import { cn } from "@/lib/utils";

type TabSpec = {
  slug: string;
  label: string;
  exact?: boolean;
};

const TABS: TabSpec[] = [
  { slug: "", label: "Overview", exact: true },
  { slug: "lifecycle", label: "Lifecycle" },
  { slug: "policy", label: "Policy" },
  { slug: "replication", label: "Replication" },
  { slug: "versioning", label: "Versioning" },
  { slug: "object-lock", label: "Object Lock" },
  { slug: "object-keys", label: "Object Keys" },
];

export function BucketTabs({
  tenantId,
  backendId,
  bucketName,
}: {
  tenantId: string;
  backendId: string;
  bucketName: string;
}) {
  const pathname = usePathname();
  const base = `/tenants/${tenantId}/buckets/${encodeURIComponent(
    backendId,
  )}/${encodeURIComponent(bucketName)}`;

  return (
    <div
      className="flex w-full items-center gap-0.5 overflow-x-auto border-b border-border/60"
      role="tablist"
    >
      {TABS.map((tab) => {
        const href = tab.slug ? `${base}/${tab.slug}` : base;
        const active = tab.exact
          ? pathname === href
          : pathname === href || pathname.startsWith(`${href}/`);
        return (
          <Link
            key={tab.slug || "overview"}
            href={href}
            role="tab"
            aria-selected={active}
            className={cn(
              "shrink-0 px-3 py-2 text-sm font-medium transition-colors",
              "border-b-2 -mb-px",
              active
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground hover:border-border",
            )}
          >
            {tab.label}
          </Link>
        );
      })}
    </div>
  );
}
