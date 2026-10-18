"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { ShareIcon, XMarkIcon } from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { type FailedRead, ListLoadError } from "@/components/ui/ListLoadError";
import { useNotification } from "@/components/ui/Notification";
import { SortableHead } from "@/components/ui/SortHeader";
import { TableToolbar } from "@/components/ui/TableToolbar";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  TenantSelect,
  tenantOptionLabel,
  type TenantOption,
} from "@/components/features/tenants/TenantSelect";
import type { BucketGrant } from "@/gen/paladin/admin/v1/bucket_service_pb";
import { ErrorReason } from "@/gen/paladin/common/v1/error_reason_pb";
import { errorMessage, errorReason } from "@/hooks/errorContract";
import { useBucketGrants } from "@/hooks/useBucketGrants";
import { useTableView } from "@/hooks/useTableView";
import type { SearchText, SortAccessors } from "@/lib/table/view";
import { formatDateTime } from "@/lib/format/locale";
import { T } from "@/lib/ui/typography";
import { timestampToDate } from "@/lib/utils";

type GrantColumn = "tenant" | "by" | "at";

// The columns of the grants table; the no-match row spans them.
const GRANT_TABLE_COLUMNS = 4;

/** What the operator reads when a revoke is refused for the tenant's collections. */
export const GRANT_IN_USE_MESSAGE =
  "The tenant still has collections in this bucket. Delete or move them first; a grant is not revoked while they remain.";

/** Names the picker of the tenant to grant. */
export const GRANT_PICKER_LABEL = "Tenant to grant";

/** Refusals with a plainer explanation than the server's message. */
const REASON_MESSAGES: Partial<Record<ErrorReason, string>> = {
  [ErrorReason.BUCKET_GRANT_IN_USE]: GRANT_IN_USE_MESSAGE,
};

/** The tenant overview, where a resolved tenant links. */
const tenantHref = (t: TenantOption) =>
  `/tenants/${encodeURIComponent(t.slug || t.tenantId)}`;

/**
 * "Shared with" — the tenants other than its owner that may bind Collections
 * to a bucket (ADR-0032). The owner grants a tenant here and revokes it; a
 * revoke is refused while the tenant still has collections in the bucket.
 */
export function BucketGrantsCard({
  bucketName,
  ownerTenantId,
  tenants,
  tenantsFailed = null,
  onChanged,
}: {
  /** `storageBackends/{backend_id}/buckets/{bucket_id}`. */
  bucketName: string;
  /** The bucket's owner, offered neither as a grantee nor in the list. */
  ownerTenantId: string;
  /** Every tenant, to name grantees and to pick a new one from. */
  tenants: TenantOption[];
  /** Set when the tenant list failed to load: names fall back to UUIDs. */
  tenantsFailed?: FailedRead | null;
  /** Called after a grant or a revoke lands, for what else shows the grants. */
  onChanged?: () => void;
}) {
  const { showNotification } = useNotification();
  const {
    grants,
    loading,
    error,
    fetchGrants,
    grantBucket,
    revokeBucketGrant,
  } = useBucketGrants(bucketName);
  const [pick, setPick] = useState("");
  const [granting, setGranting] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<BucketGrant | null>(null);
  // A refused grant or revoke, kept beside the list it is about: the confirm
  // dialog closes once its action returns.
  const [actionError, setActionError] = useState<string | null>(null);

  useEffect(() => {
    void fetchGrants();
  }, [fetchGrants]);

  const tenantByID = useMemo(
    () => new Map(tenants.map((t) => [t.tenantId, t])),
    [tenants],
  );
  // A grantee sorts and is searched by the name it shows under, which only
  // the tenant list knows.
  const grantee = useMemo(
    () => (g: BucketGrant) => {
      const t = tenantByID.get(g.tenantId);
      return t ? t.displayName || t.slug : g.tenantId;
    },
    [tenantByID],
  );
  const sortBy = useMemo<SortAccessors<BucketGrant, GrantColumn>>(
    () => ({
      tenant: grantee,
      by: (g) => g.grantedBy || undefined,
      at: (g) => (g.grantedAt ? timestampToDate(g.grantedAt) : undefined),
    }),
    [grantee],
  );
  const searchIn = useMemo<SearchText<BucketGrant>>(
    () => (g) => [
      grantee(g),
      tenantByID.get(g.tenantId)?.slug,
      g.tenantId,
      g.grantedBy,
    ],
    [grantee, tenantByID],
  );
  const view = useTableView(grants, { text: searchIn, accessors: sortBy });
  const sortProps = { current: view.sort, onSort: view.toggleSort };

  // Neither the owner nor a tenant already granted can be granted again.
  const grantable = useMemo(() => {
    const taken = new Set([ownerTenantId, ...grants.map((g) => g.tenantId)]);
    return tenants.filter((t) => !taken.has(t.tenantId));
  }, [tenants, grants, ownerTenantId]);
  // A tenant picked, then granted, is no longer on offer.
  const picked = grantable.some((t) => t.tenantId === pick) ? pick : "";

  const tenantName = (tenantId: string) => {
    const t = tenantByID.get(tenantId);
    return t ? tenantOptionLabel(t) : tenantId;
  };

  const explain = (err: unknown, fallback: string) =>
    REASON_MESSAGES[errorReason(err)] ?? errorMessage(err, fallback);

  const handleGrant = async () => {
    if (!picked) return;
    setActionError(null);
    setGranting(true);
    try {
      await grantBucket(picked);
      onChanged?.();
      const t = tenantByID.get(picked);
      showNotification({
        type: "success",
        title: "Bucket granted",
        message: t ? tenantOptionLabel(t) : picked,
      });
      setPick("");
    } catch (err) {
      setActionError(explain(err, "Failed to grant the bucket."));
    } finally {
      setGranting(false);
    }
  };

  const handleRevoke = async () => {
    if (!revokeTarget) return;
    setActionError(null);
    try {
      await revokeBucketGrant(revokeTarget.tenantId);
      onChanged?.();
      showNotification({
        type: "success",
        title: "Grant revoked",
        message: tenantName(revokeTarget.tenantId),
      });
    } catch (err) {
      setActionError(explain(err, "Failed to revoke the grant."));
    }
  };

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-base">
          <ShareIcon className="size-5 text-chart-5" />
          Shared with
          <span className="text-xs font-normal text-muted-foreground">
            Tenants other than the owner that may bind collections here
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3 pt-0">
        {tenantsFailed && (
          <ListLoadError
            variant="inline"
            what="Tenants"
            reason={tenantsFailed.reason}
            onRetry={tenantsFailed.retry}
          />
        )}
        <div className="flex flex-wrap items-center gap-2">
          <div className="min-w-60 flex-1">
            <TenantSelect
              value={picked}
              onValueChange={setPick}
              tenants={grantable}
              placeholder={
                grantable.length === 0
                  ? "No other tenant to grant"
                  : "Pick a tenant to grant"
              }
              label={{ "aria-label": GRANT_PICKER_LABEL }}
            />
          </div>
          <Button
            size="sm"
            onClick={() => void handleGrant()}
            disabled={!picked || granting}
          >
            {granting ? "Granting…" : "Grant"}
          </Button>
        </div>
        {actionError && (
          <p role="alert" className="text-sm text-destructive">
            {actionError}
          </p>
        )}
        {error ? (
          <ListLoadError
            what="Grants"
            reason={error}
            onRetry={() => void fetchGrants()}
          />
        ) : grants.length === 0 ? (
          <p className={T.helper}>
            {loading
              ? "Loading grants…"
              : "Not shared: only the owner binds collections here."}
          </p>
        ) : (
          <div className="space-y-3">
            <TableToolbar
              query={view.query}
              onQueryChange={view.setQuery}
              searchLabel="Search grants"
              placeholder="Search by tenant or granter…"
              count={{ shown: view.rows.length, total: view.total }}
            />
            <Table>
              <TableHeader>
                <TableRow>
                  <SortableHead label="Tenant" column="tenant" {...sortProps} />
                  <SortableHead
                    label="Granted by"
                    column="by"
                    className="hidden @md:table-cell"
                    {...sortProps}
                  />
                  <SortableHead
                    label="Granted at"
                    column="at"
                    className="hidden @md:table-cell"
                    {...sortProps}
                  />
                  <TableHead className="w-12 text-right">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {view.rows.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={GRANT_TABLE_COLUMNS}
                      className="py-6 text-center text-muted-foreground"
                    >
                      No grant matches the search.
                    </TableCell>
                  </TableRow>
                ) : null}
                {view.rows.map((g) => {
                  const t = tenantByID.get(g.tenantId);
                  return (
                    <TableRow key={g.tenantId}>
                      <TableCell>
                        {t ? (
                          <Link
                            href={tenantHref(t)}
                            className="flex items-center gap-2 hover:text-primary"
                          >
                            {t.displayName || t.slug}
                            {t.slug && (
                              <Badge variant="outline">{t.slug}</Badge>
                            )}
                          </Link>
                        ) : (
                          <span className={T.code}>{g.tenantId}</span>
                        )}
                      </TableCell>
                      <TableCell className="hidden @md:table-cell">
                        <span className={T.code}>{g.grantedBy || "—"}</span>
                      </TableCell>
                      <TableCell className="hidden @md:table-cell text-xs text-muted-foreground">
                        {g.grantedAt
                          ? formatDateTime(timestampToDate(g.grantedAt))
                          : "—"}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-8"
                          aria-label={`Revoke ${tenantName(g.tenantId)}`}
                          onClick={() => setRevokeTarget(g)}
                        >
                          <XMarkIcon className="size-4" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>

      <ConfirmModal
        isOpen={revokeTarget !== null}
        onClose={() => setRevokeTarget(null)}
        onConfirm={handleRevoke}
        type="danger"
        title="Revoke this grant?"
        message={
          <p>
            <span className="text-foreground">
              {revokeTarget ? tenantName(revokeTarget.tenantId) : ""}
            </span>{" "}
            may no longer bind collections to this bucket.
          </p>
        }
        warning="Refused while the tenant still has collections in the bucket: remove them first. Once revoked, its new collections cannot be bound here."
        confirmText="Revoke grant"
      />
    </Card>
  );
}
