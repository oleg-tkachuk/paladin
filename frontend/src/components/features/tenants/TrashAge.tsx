"use client";

// TrashAge — how long a tenant has sat in the trash, and whether its retention
// has lapsed. The server owns the retention (admin.tenants.trash_retention)
// and reports its result as Tenant.purge_eligible_at = deleted_at +
// retention, so nothing here knows the period: a tenant is past retention
// from that instant on (inclusive). Past retention means surfaced for purge,
// never purged — the operator still decides.

import type { Timestamp } from "@bufbuild/protobuf/wkt";

import { Badge } from "@/components/ui/badge";
import { formatDateTime } from "@/lib/format/locale";
import { T } from "@/lib/ui/typography";

const DAY_MS = 24 * 60 * 60 * 1000;

function toDate(ts: Timestamp | undefined): Date | null {
  if (!ts) return null;
  return new Date(Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1e6));
}

export interface TrashAgeInfo {
  /** When the tenant was trashed. */
  deleted: Date;
  /** Whole days since the tenant was trashed (never negative). */
  days: number;
  /** When the retention lapses; null when the server reported none. */
  eligibleAt: Date | null;
  /** True from eligibleAt on, inclusive. */
  pastRetention: boolean;
  /** Whole days, rounded up, until eligibleAt; null once past or unknown. */
  daysLeft: number | null;
}

/** null when the tenant carries no deleted_at — it is not in the trash. */
export function trashAge(
  deletedAt: Timestamp | undefined,
  purgeEligibleAt: Timestamp | undefined,
  now: Date,
): TrashAgeInfo | null {
  const deleted = toDate(deletedAt);
  if (!deleted) return null;
  const eligibleAt = toDate(purgeEligibleAt);
  const days = Math.max(
    0,
    Math.floor((now.getTime() - deleted.getTime()) / DAY_MS),
  );
  const pastRetention =
    eligibleAt !== null && now.getTime() >= eligibleAt.getTime();
  const daysLeft =
    eligibleAt !== null && !pastRetention
      ? Math.ceil((eligibleAt.getTime() - now.getTime()) / DAY_MS)
      : null;
  return { deleted, days, eligibleAt, pastRetention, daysLeft };
}

export function trashAgeLabel(days: number): string {
  if (days < 1) return "In trash less than a day";
  return `In trash ${days} ${days === 1 ? "day" : "days"}`;
}

export interface TrashAgeProps {
  deletedAt: Timestamp | undefined;
  purgeEligibleAt: Timestamp | undefined;
  /** Injected by tests; defaults to the time of render. */
  now?: Date;
}

export function TrashAge({ deletedAt, purgeEligibleAt, now }: TrashAgeProps) {
  const info = trashAge(deletedAt, purgeEligibleAt, now ?? new Date());
  if (!info) {
    return <span title="No deletion time recorded">—</span>;
  }
  const { deleted, days, eligibleAt, pastRetention, daysLeft } = info;
  return (
    <div className="space-y-1">
      <p title={`Trashed ${formatDateTime(deleted)}`}>{trashAgeLabel(days)}</p>
      {pastRetention && eligibleAt && (
        <Badge
          variant="warning"
          title={`Retention lapsed ${formatDateTime(eligibleAt)}. Nothing is purged automatically — restore or purge it.`}
        >
          Past retention
        </Badge>
      )}
      {daysLeft !== null && (
        <p className={T.helper}>
          Purge-eligible in {daysLeft} {daysLeft === 1 ? "day" : "days"}
        </p>
      )}
    </div>
  );
}
