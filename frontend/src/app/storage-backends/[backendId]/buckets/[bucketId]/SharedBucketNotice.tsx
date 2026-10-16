"use client";

import { usePathname } from "next/navigation";
import { UsersIcon } from "@heroicons/react/24/outline";

import { isBucketSettingsTab } from "@/components/layout/BucketTabs";

import { useBucket } from "./bucket-context";

/** What the notice says, for a bucket shared with `count` tenants. */
export function sharedSettingsMessage(count: number, owner: string): string {
  const tenants = count === 1 ? "1 other tenant" : `${count} other tenants`;
  return `Shared with ${tenants}: a setting changed here applies to them as well as to ${owner}.`;
}

/**
 * On a settings tab of a bucket granted to other tenants, says that a change
 * reaches them too (ADR-0033) — the settings are the bucket's, not the
 * owner's alone.
 */
export function SharedBucketNotice() {
  const pathname = usePathname();
  const { bucket, owner } = useBucket();
  const count = bucket.grantedTenantIds.length;
  if (count === 0 || !isBucketSettingsTab(pathname)) return null;
  const ownerName = owner?.displayName || owner?.slug || "its owner";
  return (
    <div
      role="note"
      className="flex items-start gap-2 rounded-md border border-chart-3/40 bg-chart-3/10 p-3 text-sm text-foreground"
    >
      <UsersIcon className="mt-0.5 size-4 shrink-0 text-chart-3" />
      <span>{sharedSettingsMessage(count, ownerName)}</span>
    </div>
  );
}
