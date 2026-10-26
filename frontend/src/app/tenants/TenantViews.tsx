"use client";

// The tenants list's other views (lib/table/viewMode): a tile per tenant,
// and a summary of how the tenants split. Both show the rows the table
// would, in its order, after its search.
import Link from "next/link";
import {
  BuildingOfficeIcon,
  CircleStackIcon,
  ServerStackIcon,
  ShieldCheckIcon,
  TagIcon,
} from "@heroicons/react/24/outline";

import { Breakdown, stateEntries } from "@/components/ui/Breakdown";
import { tallyStatus } from "@/components/ui/StatusBar";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { NoticeIcon } from "@/components/ui/Notice";
import { StatusLabel } from "@/components/ui/StatusIcon";
import { Button } from "@/components/ui/button";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { formatCount } from "@/lib/format/locale";
import { parseBucketName, tenantHref } from "@/lib/navigation/hrefs";
import { noticeFromLabels } from "@/lib/notice";
import { isDedicatedLayout } from "@/lib/storageLayout";
import { tenantLimitState, type TenantLimitSignals } from "@/lib/tenant/limits";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { plainLabels, tenantBreakdowns, tenantStatus } from "./tenantView";

/** The tenant pages a tile links straight to. */
const QUICK_LINKS = [
  { tab: "collections", label: "Collections" },
  { tab: "buckets", label: "Buckets" },
  { tab: "quotas", label: "Quotas" },
  { tab: "policies", label: "Policies" },
] as const;

/** Each tenant's limit signals by id; a tenant absent from it shows none. */
type Limits = ReadonlyMap<string, TenantLimitSignals>;

/** The grid tiles and summary cards are laid out in. */
const GRID = "grid gap-4 sm:grid-cols-2 xl:grid-cols-3";

export function TenantTiles({
  tenants,
  limits,
}: {
  tenants: readonly Tenant[];
  limits: Limits;
}) {
  return (
    <ul className={GRID} aria-label="Tenants">
      {tenants.map((t) => (
        <li key={t.tenantId}>
          <TenantTile tenant={t} limits={limits.get(t.tenantId)} />
        </li>
      ))}
    </ul>
  );
}

function TenantTile({
  tenant: t,
  limits,
}: {
  tenant: Tenant;
  limits: TenantLimitSignals | undefined;
}) {
  // Slug-first, as the table links; the id stands in for a legacy row.
  const handle = t.slug || t.tenantId;
  const href = tenantHref(handle);
  const status = tenantStatus(t, limits);
  // A notice that leads at the same tone does not hide the limit state.
  const limit = tenantLimitState(limits);
  const notice = noticeFromLabels(t.labels);
  const bucket = parseBucketName(t.defaultBucket);
  return (
    <Card size="sm" className="h-full">
      <CardHeader>
        <CardTitle icon={BuildingOfficeIcon} className="min-w-0">
          <Link
            href={href}
            className="min-w-0 truncate hover:text-primary hover:underline"
          >
            {t.displayName || handle}
          </Link>
          {notice && <NoticeIcon notice={notice} />}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className={cn(T.codeSmall, "truncate")}>{handle}</span>
          <span className="flex flex-wrap items-center gap-2">
            <StatusLabel tone={status.tone}>{status.label}</StatusLabel>
            {limit && limit.label !== status.label && (
              <StatusLabel tone={limit.tone}>{limit.label}</StatusLabel>
            )}
          </span>
        </div>
        <dl className="grid grid-cols-3 gap-2 text-sm">
          <div>
            <dt className={T.hint}>Layout</dt>
            <dd>
              {isDedicatedLayout(t.storageLayout) ? "Dedicated" : "Shared"}
            </dd>
          </div>
          <div className="min-w-0">
            <dt className={T.hint}>Bucket</dt>
            <dd className={cn(T.codeSmall, "truncate")} title={t.defaultBucket}>
              {bucket?.bucketId ?? "—"}
            </dd>
          </div>
          <div>
            <dt className={T.hint}>Labels</dt>
            <dd className="tabular-nums">
              {formatCount(plainLabels(t.labels).length)}
            </dd>
          </div>
        </dl>
        <div className="flex flex-wrap gap-1">
          {QUICK_LINKS.map((l) => (
            <Button key={l.tab} variant="ghost" size="xs" asChild>
              <Link href={`${href}/${l.tab}`}>{l.label}</Link>
            </Button>
          ))}
        </div>
      </CardContent>
    </Card>
  );
}

export function TenantSummary({
  tenants,
  limits,
}: {
  tenants: readonly Tenant[];
  limits: Limits;
}) {
  const { layout, backend, labels } = tenantBreakdowns(tenants);
  const states = stateEntries(
    tallyStatus(tenants, (t) => tenantStatus(t, limits.get(t.tenantId))),
  );
  return (
    <div className={GRID}>
      <Breakdown title="State" icon={ShieldCheckIcon} entries={states} />
      <Breakdown
        title="Storage layout"
        icon={CircleStackIcon}
        entries={layout}
      />
      <Breakdown
        title="Default backend"
        icon={ServerStackIcon}
        entries={backend}
      />
      {labels.length > 0 && (
        <Breakdown title="Label keys" icon={TagIcon} entries={labels} />
      )}
    </div>
  );
}
