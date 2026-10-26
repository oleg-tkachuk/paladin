// What the tenants list says about a tenant beyond its row: the state it
// leads with (its status bar, its tile) and how the list splits (its
// summary). Pure, so the views can be tested without rendering the page.

import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { parseBucketName } from "@/lib/navigation/hrefs";
import { isNoticeLabel, noticeFromLabels } from "@/lib/notice";
import { isDedicatedLayout } from "@/lib/storageLayout";
import { tallyBy, type Tally } from "@/lib/table/viewMode";
import { tenantLimitState, type TenantLimitSignals } from "@/lib/tenant/limits";
import { STATUS_ORDER, StatusTone } from "@/lib/ui/status";

type TenantFields = Pick<
  Tenant,
  "deletedAt" | "labels" | "storageLayout" | "defaultBucket"
>;

/** The state a tenant leads with: what its status bar counts and its tile says. */
type TenantState = { label: string; tone: StatusTone };

const FROZEN: TenantState = { label: "Frozen", tone: StatusTone.Suspended };
const ACTIVE: TenantState = { label: "Active", tone: StatusTone.Success };

/** The state a warning or critical notice gives a tenant; info is news. */
function noticeState(labels: Readonly<Record<string, string>>) {
  switch (noticeFromLabels(labels)?.severity) {
    case "critical":
      return { label: "Critical notice", tone: StatusTone.Danger };
    case "warning":
      return { label: "Warning notice", tone: StatusTone.Warning };
    default:
      return undefined;
  }
}

/**
 * The one state a tenant leads with. A tenant in the trash is frozen, a
 * pause and not a fault, whatever else holds. Otherwise the most urgent of
 * its notice and its limit state (lib/tenant/limits) by tone, danger before
 * warning; at the same tone the notice its operator pinned leads, and its
 * limit state still shows on its row. A tenant with neither is active.
 */
export function tenantStatus(
  t: Pick<TenantFields, "deletedAt" | "labels">,
  limits?: TenantLimitSignals,
): TenantState {
  if (t.deletedAt) return FROZEN;
  const candidates = [noticeState(t.labels), tenantLimitState(limits)].filter(
    (s): s is TenantState => s !== undefined,
  );
  if (candidates.length === 0) return ACTIVE;
  const rank = (s: TenantState) => STATUS_ORDER.indexOf(s.tone);
  // A stable sort keeps the notice first at the same tone.
  return [...candidates].sort((a, b) => rank(a) - rank(b))[0];
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
