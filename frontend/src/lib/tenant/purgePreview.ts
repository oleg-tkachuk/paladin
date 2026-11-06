// What a tenant purge would remove and what would refuse it, as the purge
// confirmation lists it, from TenantService.PreviewPurgeTenant.
import type { Affected } from "@/components/ui/ConfirmModal";
import {
  PurgeBlockerKind,
  type PreviewPurgeTenantResponse,
  type PurgeBlocker,
} from "@/gen/paladin/admin/v1/tenant_service_pb";
import { formatCount } from "@/lib/format/locale";

/** What each blocker kind is called, as a count of it reads. */
const BLOCKER_NOUNS: Record<PurgeBlockerKind, [one: string, many: string]> = {
  [PurgeBlockerKind.UNSPECIFIED]: ["row", "rows"],
  [PurgeBlockerKind.COLLECTIONS]: ["collection", "collections"],
  [PurgeBlockerKind.BUCKETS]: ["bucket", "buckets"],
  [PurgeBlockerKind.USERS]: ["user", "users"],
  [PurgeBlockerKind.BUCKETS_DELETING]: [
    "bucket being deleted",
    "buckets being deleted",
  ],
};

/** one for a count of one, many for any other. */
function oneOrMany<T>(n: bigint, one: T, many: T): T {
  return n === 1n ? one : many;
}

function counted(n: bigint, [one, many]: [string, string]): string {
  return `${formatCount(n)} ${oneOrMany(n, one, many)}`;
}

/** What a purge removes that the preview neither names nor counts. */
export const PURGE_ALSO_REMOVED: readonly string[] = [
  "Quotas, rate limits and slug history",
  "Operations, object locks and upload sessions",
];

/**
 * What a purge removes with the tenant, by kind, for a confirmation that has
 * no preview to read: still loading, or refused.
 */
export const PURGE_CASCADE: Affected = {
  heading: "Removed with it",
  items: [
    "API tokens: integrations using them stop at once",
    "Event subscriptions: deliveries stop, silently",
    "Capabilities, with their counters and charges",
    ...PURGE_ALSO_REMOVED,
  ],
  mono: false,
};

/**
 * The lists the confirmation shows from a preview: the tokens and
 * subscriptions by name, with how many more there are, then the rest by
 * kind, the capabilities counted. A named kind the tenant holds none of is
 * left out.
 */
export function affectedByPurge(p: PreviewPurgeTenantResponse): Affected[] {
  const out: Affected[] = [];
  if (p.apiTokenCount > 0n) {
    out.push({
      heading: `${counted(p.apiTokenCount, ["API token", "API tokens"])}: integrations using ${oneOrMany(p.apiTokenCount, "it", "them")} stop at once`,
      items: p.apiTokens.map((t) => t.displayName || t.name),
      total: Number(p.apiTokenCount),
    });
  }
  if (p.eventSubscriptionCount > 0n) {
    out.push({
      heading: `${counted(p.eventSubscriptionCount, ["event subscription", "event subscriptions"])}: deliveries stop, silently`,
      items: p.eventSubscriptions.map((s) =>
        s.displayName ? `${s.displayName} · ${s.name}` : s.name,
      ),
      total: Number(p.eventSubscriptionCount),
    });
  }
  const rest =
    p.capabilityCount > 0n
      ? [
          `${counted(p.capabilityCount, ["capability", "capabilities"])}, with ${oneOrMany(p.capabilityCount, "its", "their")} counters and charges`,
          ...PURGE_ALSO_REMOVED,
        ]
      : PURGE_ALSO_REMOVED;
  out.push({ heading: "Also removed", items: rest, mono: false });
  return out;
}

/** Why rows the operator has to remove refuse the purge. */
function toRemove(blockers: readonly PurgeBlocker[]): string {
  const owned = blockers
    .map((b) => counted(b.count, BLOCKER_NOUNS[b.kind]))
    .join(", ");
  const total = blockers.reduce((sum, b) => sum + b.count, 0n);
  const [it, is] = oneOrMany(total, ["it", "it is"], ["them", "they are"]);
  return `The tenant still owns ${owned}. The purge is refused until ${is} removed: restore the tenant, delete ${it}, and trash it again.`;
}

/** Why buckets still being deleted on their backend refuse the purge. */
function toWaitFor(deleting: bigint): string {
  const [noun, is] = oneOrMany(
    deleting,
    ["bucket is", "it finishes"],
    ["buckets are", "they finish"],
  );
  return `${formatCount(deleting)} ${noun} still being deleted on the storage backend. Nothing needs doing: retry the purge once ${is}.`;
}

/**
 * Why the purge would be refused: what the tenant still owns and has to
 * remove, then the buckets it only has to wait for; null when nothing
 * refuses it.
 */
export function whatBlocksPurge(
  blockers: readonly PurgeBlocker[],
): string | null {
  const owned = blockers.filter(
    (b) => b.kind !== PurgeBlockerKind.BUCKETS_DELETING,
  );
  const deleting = blockers
    .filter((b) => b.kind === PurgeBlockerKind.BUCKETS_DELETING)
    .reduce((sum, b) => sum + b.count, 0n);
  const reasons = [
    ...(owned.length > 0 ? [toRemove(owned)] : []),
    ...(deleting > 0n ? [toWaitFor(deleting)] : []),
  ];
  return reasons.length > 0 ? reasons.join(" ") : null;
}
