// What the tenants list says about a tenant beyond its row: the state it
// leads with (its status bar, its tile) and how the list splits (its
// summary). Pure, so the views can be tested without rendering the page.

import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { parseBucketName } from "@/lib/navigation/hrefs";
import { isNoticeLabel, noticeFromLabels } from "@/lib/notice";
import { isDedicatedLayout } from "@/lib/storageLayout";
import { tallyBy, type Tally } from "@/lib/table/viewMode";
import { StatusTone } from "@/lib/ui/status";

type TenantFields = Pick<
  Tenant,
  "deletedAt" | "labels" | "storageLayout" | "defaultBucket"
>;

/**
 * The one state a tenant leads with. A tenant in the trash is frozen, a
 * pause and not a fault; otherwise a warning or critical notice its
 * operator pinned to it says it needs a look. The list reads no quota or
 * budget usage, so a tenant near its limits is not told apart here.
 */
export function tenantStatus(t: Pick<TenantFields, "deletedAt" | "labels">): {
  label: string;
  tone: StatusTone;
} {
  if (t.deletedAt) return { label: "Frozen", tone: StatusTone.Suspended };
  switch (noticeFromLabels(t.labels)?.severity) {
    case "critical":
      return { label: "Critical notice", tone: StatusTone.Danger };
    case "warning":
      return { label: "Warning notice", tone: StatusTone.Warning };
    default:
      return { label: "Active", tone: StatusTone.Success };
  }
}

/** The labels a tenant shows as its own: a notice's are its glyph instead. */
export function plainLabels(labels: Readonly<Record<string, string>>) {
  return Object.entries(labels).filter(([k]) => !isNoticeLabel(k));
}

/** How the tenants split, one tally per question the summary answers. */
export function tenantBreakdowns(tenants: readonly TenantFields[]): {
  layout: Tally[];
  backend: Tally[];
  labels: Tally[];
} {
  return {
    layout: tallyBy(
      tenants,
      (t) => (isDedicatedLayout(t.storageLayout) ? "Dedicated" : "Shared"),
      "Shared",
    ),
    backend: tallyBy(
      tenants,
      (t) => parseBucketName(t.defaultBucket)?.backendId,
      "No default bucket",
    ),
    // A tenant counts once under each label key it carries.
    labels: tallyBy(
      tenants.flatMap((t) => plainLabels(t.labels).map(([k]) => k)),
      (k) => k,
      "",
    ),
  };
}
