"use client";

// TrashAge — how long a tenant has sat in the trash, and how much of its
// retention window that has used. The server owns the retention (admin.tenants.trash_retention)
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

/** How long ago the tenant was trashed, in whole days. */
export function trashedAgo(days: number): string {
  if (days < 1) return "today";
  return `${days} ${days === 1 ? "day" : "days"} ago`;
}

export interface TrashAgeProps {
  deletedAt: Timestamp | undefined;
  purgeEligibleAt: Timestamp | undefined;
  /** Injected by tests; defaults to the time of render. */
  now?: Date;
}

/**
 * How much of the retention window has passed, from 0 to 1; null when the
 * server reported no purge-eligible time. Past retention is 1.
 */
export function retentionElapsed(info: TrashAgeInfo, now: Date): number | null {
  if (!info.eligibleAt) return null;
  const window = info.eligibleAt.getTime() - info.deleted.getTime();
  if (window <= 0) return 1;
  const elapsed = (now.getTime() - info.deleted.getTime()) / window;
  return Math.min(1, Math.max(0, elapsed));
}

/** Whole days in the retention window; null when the server reported none. */
export function retentionDays(info: TrashAgeInfo): number | null {
  if (!info.eligibleAt) return null;
  return Math.round(
    (info.eligibleAt.getTime() - info.deleted.getTime()) / DAY_MS,
  );
}

function daysWord(n: number): string {
  return `${n} ${n === 1 ? "day" : "days"}`;
}

/**
 * The retention window as a bar: how much of it the tenant has used, and what
 * is left. Past retention reads as a warning badge — the tenant is due for a
 * decision, never purged unattended.
 */
export function TrashRetention({
  deletedAt,
  purgeEligibleAt,
  now,
}: TrashAgeProps) {
  const at = now ?? new Date();
  const info = trashAge(deletedAt, purgeEligibleAt, at);
  const elapsed = info ? retentionElapsed(info, at) : null;
  if (!info || elapsed === null || !info.eligibleAt) {
    return <span className={T.hint}>No retention reported</span>;
  }
  const eligible = formatDateTime(info.eligibleAt);
  return (
    <div
      className="w-full max-w-48 space-y-1.5"
      title={`Purge-eligible ${eligible}`}
    >
      {info.pastRetention ? (
        <Badge variant="warning">Past retention</Badge>
      ) : (
        <p className="text-sm tabular-nums">
          {daysWord(info.daysLeft ?? 0)} left
        </p>
      )}
      <div
        role="progressbar"
        aria-label="Retention used"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(elapsed * 100)}
        className="h-1 w-full overflow-hidden rounded-full bg-muted"
      >
        <div
          className={
            info.pastRetention ? "h-full bg-warning" : "h-full bg-primary/70"
          }
          style={{ width: `${elapsed * 100}%` }}
        />
      </div>
    </div>
  );
}
