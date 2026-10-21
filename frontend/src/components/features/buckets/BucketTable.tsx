"use client";

import { bucketDetailQuery } from "@/lib/queries/details";
import { IntentTableRow } from "@/components/ui/IntentTableRow";
import Link from "next/link";
import { ArchiveBoxIcon } from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/Card";
import { NoticeIcon } from "@/components/ui/Notice";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import type { SortState } from "@/hooks/useTableSort";
import { backendHref, bucketHref, tenantHref } from "@/lib/navigation/hrefs";
import type { Facet } from "@/lib/table/facets";
import { StatusTone } from "@/lib/ui/status";
import { noticeFromLabels } from "@/lib/notice";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

import { BucketTenancyBadge } from "./BucketTenancyBadge";
import { ProvisionStateBadge } from "./ProvisionStateBadge";
import { PublicReadBadge } from "./PublicReadBadge";
import { TenantHoverCard } from "@/components/features/hover/ResourceHoverCards";

/** The columns a bucket list sorts by. */
export const BUCKET_SORT_COLUMNS = ["backend", "name"] as const;
export type BucketSortColumn = (typeof BUCKET_SORT_COLUMNS)[number];

/** How many grantees a row names before it counts the rest. */
export const GRANTEES_NAMED = 2;
/** What the shared-with column says for a bucket nobody was granted. */
export const NOT_SHARED_LABEL = "owner only";
/** How many skeleton rows stand in while the first page loads. */
const SKELETON_ROWS = 3;
/**
 * A cell of tenant names: one line, cut with an ellipsis, the full text on
 * hover. A tenant's display name is free text, and a long one widened the
 * table until its state and actions left the screen.
 */
const NAME_CELL_CLASS = "block max-w-56 truncate";
/** The columns every row has: bucket, owner, shared with, state. */
const FIXED_COLUMNS = 4;

/** A tenant as a bucket row names it. */
export interface TenantLabel {
  slug: string;
  displayName: string;
}

export interface BucketTableProps {
  buckets: readonly Bucket[];
  /** The tenants the rows name, by tenant id; an unknown one shows its id. */
  tenants: ReadonlyMap<string, TenantLabel>;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
  /** What the table says when there are no rows. */
  empty: React.ReactNode;
  /** The tenant whose list this is: its rows say owned or granted. */
  perspective?: string;
  sort?: SortState<BucketSortColumn>;
  onSort?: (column: BucketSortColumn) => void;
  /** The trailing cell of a row: its actions. */
  rowActions?: (bucket: Bucket) => React.ReactNode;
}

/** The tenants a list knows, keyed for the rows. */
export function tenantLabelMap(
  tenants: readonly ({ tenantId: string } & TenantLabel)[],
): ReadonlyMap<string, TenantLabel> {
  return new Map(
    tenants.map((t) => [
      t.tenantId,
      { slug: t.slug, displayName: t.displayName },
    ]),
  );
}

/** The provisioning state a bucket from before the state was kept reads as. */
const READY = "ready";

/**
 * The facets a list of buckets filters by: the backend it lives on (the ones
 * the loaded buckets name), whether it is shared (ADR-0032 made who it is
 * shared with the fact its settings reach), and where its provisioning
 * stands, toned as ProvisionStateBadge names it.
 */
export const BUCKET_FACETS: readonly Facet<Bucket>[] = [
  { id: "backend", label: "Backend", values: (b) => [b.backendId] },
  {
    id: "sharing",
    label: "Sharing",
    values: (b) => [b.grantedTenantIds.length > 0 ? "shared" : "owner-only"],
    options: [
      { value: "shared", label: "Shared" },
      { value: "owner-only", label: "Owner only" },
    ],
  },
  {
    id: "state",
    label: "State",
    values: (b) => [b.provisionState || READY],
    options: [
      { value: READY, label: "Ready", tone: StatusTone.Success },
      { value: "pending", label: "Provisioning", tone: StatusTone.Progress },
      { value: "deleting", label: "Deleting", tone: StatusTone.Progress },
      { value: "failed", label: "Failed", tone: StatusTone.Danger },
      {
        value: "deletion_failed",
        label: "Delete failed",
        tone: StatusTone.Danger,
      },
    ],
  },
];

/** The name a row gives a tenant: its display name, its slug, its id. */
export function tenantLabel(
  tenants: ReadonlyMap<string, TenantLabel>,
  id: string,
): string {
  const t = tenants.get(id);
  return t?.displayName || t?.slug || id;
}

/** Whether the rows span more than one backend. */
export function spansBackends(buckets: readonly Bucket[]): boolean {
  return new Set(buckets.map((b) => b.backendId)).size > 1;
}

/**
 * One list of buckets for every page that shows them (ADR-0033): the
 * platform's, a backend's and a tenant's. Each row says who owns the bucket
 * and who it is shared with — the tenants its settings reach — and links to
 * the bucket's one page. The backend column appears only when the rows span
 * more than one backend.
 */
export function BucketTable({
  buckets,
  tenants,
  loading,
  error,
  onRetry,
  empty,
  perspective,
  sort,
  onSort,
  rowActions,
}: BucketTableProps) {
  const showBackend = spansBackends(buckets);
  const columns = FIXED_COLUMNS + (showBackend ? 1 : 0) + (rowActions ? 1 : 0);

  const head = (label: string, column: BucketSortColumn, className = "") =>
    sort && onSort ? (
      <SortableHead
        className={className}
        label={label}
        column={column}
        current={sort}
        onSort={onSort}
      />
    ) : (
      <TableHead className={className}>{label}</TableHead>
    );

  return (
    <Card className="p-0">
      <Table>
        <TableHeader>
          <TableRow>
            {head("Bucket", "name")}
            {showBackend && head("Backend", "backend", "hidden @md:table-cell")}
            <StaticHead>Owner</StaticHead>
            <StaticHead className="hidden @lg:table-cell">
              Shared with
            </StaticHead>
            <StaticHead className="@md:w-40">State</StaticHead>
            {rowActions && (
              <TableHead className="w-12 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading && buckets.length === 0 ? (
            Array.from({ length: SKELETON_ROWS }, (_, i) => (
              <TableRow key={`s-${i}`}>
                <TableCell colSpan={columns} className="py-3">
                  <Skeleton className="h-7 w-full" />
                </TableCell>
              </TableRow>
            ))
          ) : error ? (
            // "No buckets" for a failed list invites creating one that exists.
            <TableRow>
              <TableCell colSpan={columns} className="h-48 text-center">
                <ListLoadError
                  what="Buckets"
                  reason={error}
                  onRetry={onRetry}
                />
              </TableCell>
            </TableRow>
          ) : buckets.length === 0 ? (
            <TableRow>
              <TableCell colSpan={columns} className="h-48 text-center">
                {empty}
              </TableCell>
            </TableRow>
          ) : (
            buckets.map((b) => (
              <BucketRow
                key={`${b.backendId}/${b.bucketId}`}
                bucket={b}
                tenants={tenants}
                perspective={perspective}
                showBackend={showBackend}
                actions={rowActions?.(b)}
              />
            ))
          )}
        </TableBody>
      </Table>
    </Card>
  );
}

function BucketRow({
  bucket: b,
  tenants,
  perspective,
  showBackend,
  actions,
}: {
  bucket: Bucket;
  tenants: ReadonlyMap<string, TenantLabel>;
  perspective?: string;
  showBackend: boolean;
  actions?: React.ReactNode;
}) {
  const grantees = b.grantedTenantIds;
  const named = grantees.slice(0, GRANTEES_NAMED);
  const more = grantees.length - named.length;
  const notice = noticeFromLabels(b.labels);
  return (
    <IntentTableRow
      className="group"
      query={bucketDetailQuery(b.backendId, b.bucketId)}
    >
      <TableCell>
        <div className="flex items-center gap-2">
          <Link
            href={bucketHref(b.backendId, b.bucketId)}
            className="flex min-w-0 items-center gap-3 hover:text-primary"
          >
            <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
              <ArchiveBoxIcon className="size-4" />
            </div>
            <div className="min-w-0">
              <div className={cn(T.code, "truncate group-hover:underline")}>
                {b.bucketId}
              </div>
              {b.displayName && (
                <div className={cn(T.hint, "truncate")}>{b.displayName}</div>
              )}
            </div>
          </Link>
          {/* Beside the link, not in it: the glyph takes focus itself. */}
          {notice && <NoticeIcon notice={notice} />}
        </div>
      </TableCell>
      {showBackend && (
        <TableCell className="hidden @md:table-cell">
          <Link href={backendHref(b.backendId)} className="hover:text-primary">
            <Badge variant="info" className={T.code}>
              {b.backendId}
            </Badge>
          </Link>
        </TableCell>
      )}
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          {perspective && (
            <BucketTenancyBadge owned={b.ownerTenantId === perspective} />
          )}
          <TenantHoverCard handle={b.ownerTenantId}>
            <Link
              href={tenantHref(b.ownerTenantId)}
              title={b.ownerTenantId}
              className={cn(NAME_CELL_CLASS, "text-sm hover:text-primary")}
            >
              {tenantLabel(tenants, b.ownerTenantId)}
            </Link>
          </TenantHoverCard>
        </div>
      </TableCell>
      <TableCell className="hidden @lg:table-cell text-sm">
        {grantees.length === 0 ? (
          <span className="text-muted-foreground">{NOT_SHARED_LABEL}</span>
        ) : (
          <span
            className={NAME_CELL_CLASS}
            title={grantees.map((id) => tenantLabel(tenants, id)).join(", ")}
          >
            {named.map((id) => tenantLabel(tenants, id)).join(", ")}
            {more > 0 && (
              <span className="text-muted-foreground"> +{more}</span>
            )}
          </span>
        )}
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-1">
          <ProvisionStateBadge state={b.provisionState} />
          {b.publicRead && <PublicReadBadge />}
        </div>
      </TableCell>
      {actions !== undefined && (
        <TableCell className="text-right">{actions}</TableCell>
      )}
    </IntentTableRow>
  );
}
