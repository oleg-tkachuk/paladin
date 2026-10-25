"use client";

// /trash — recovery view for soft-deleted tenants, marked by the
// `deleted_at` column (`001_initial_schema.sql`); TenantService gained RestoreTenant and
// PurgeTenant RPCs to drive recovery + hard-delete from the trash.
// This page uses ListTenants(only_trashed=true) for the source list
// and the two new RPCs for the row actions.
//
// Restore semantics:
//   - clears deleted_at, tenant returns to the active set
//   - slug/display_name UNIQUE still applies across both sets;
//     ALREADY_EXISTS surfaces a clear "rename the claimer first"
//     message
//
// Purge semantics:
//   - hard-deletes the trashed row (FAILED_PRECONDITION if active)
//   - irreversible — confirmed first, as restore is
//
// Retention: the server reports purge_eligible_at = deleted_at +
// admin.tenants.trash_retention (default 30 days). Each row shows when the
// tenant was trashed and how much of that window it has used, and is marked
// "Past retention" once the window lapses. Marked, never purged — there is no
// unattended purge.
//
// A purge the database refuses — rows still reference the tenant, a held
// object — is reported under the tenant's own row, where the operator is
// looking, and stays there until the next attempt.

// Oldest first: the tenant nearest its retention deadline leads.
function byDeletedAt(a: Tenant, b: Tenant): number {
  return (
    Number(a.deletedAt?.seconds ?? 0n) - Number(b.deletedAt?.seconds ?? 0n)
  );
}

const COLUMN_COUNT = 4;

// Which trashed tenants show, by where they stand in the retention window.
type RetentionFilter = "all" | "past" | "within";
const RETENTION_FILTERS: readonly ToolbarFilterOption<RetentionFilter>[] = [
  { value: "all", label: "All" },
  { value: "past", label: "Past retention" },
  { value: "within", label: "Within retention" },
];
const DEFAULT_RETENTION_FILTER: RetentionFilter = "all";

type TrashColumn = "tenant" | "deleted" | "retention";
const tsDate = (t: Tenant["deletedAt"]) => (t ? timestampDate(t) : undefined);
const TRASH_SORT: SortAccessors<Tenant, TrashColumn> = {
  tenant: (t) => t.displayName || t.slug,
  deleted: (t) => tsDate(t.deletedAt),
  retention: (t) => tsDate(t.purgeEligibleAt),
};
const TRASH_SEARCH = (t: Tenant) => [t.displayName, t.slug, t.tenantId];

import { HelpTip } from "@/components/ui/HelpTip";
import { Fragment, useMemo, useState } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import {
  ArchiveBoxIcon,
  ArrowUturnLeftIcon,
  ExclamationTriangleIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useTenants } from "@/hooks/useTenants";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/Card";
import { Skeleton } from "@/components/ui/Skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { usePurgePreview } from "@/hooks/usePurgePreview";
import {
  PURGE_CASCADE,
  whatBlocksPurge,
  affectedByPurge,
} from "@/lib/tenant/purgePreview";
import { SortableHead } from "@/components/ui/SortHeader";
import {
  TableToolbar,
  type ToolbarFilterOption,
} from "@/components/ui/TableToolbar";
import { useTableView } from "@/hooks/useTableView";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { useTableFilter } from "@/hooks/useTableUrl";
import type { SortAccessors } from "@/lib/table/view";
import { useNotification } from "@/components/ui/Notification";
import {
  TrashRetention,
  retentionDays,
  trashAge,
  trashedAgo,
} from "@/components/features/tenants/TrashAge";
import { IdentifierCopy } from "@/components/ui/IdentifierCopy";
import { formatDate, formatDateTime } from "@/lib/format/locale";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";
import { errorMessage } from "@/hooks/errorContract";
import { TableScope } from "@/components/ui/TableScope";

/** This list's id in the user's table preferences. */
const TABLE_ID = "trash";

export default function TrashPage() {
  // The trash, as its own cached list. Restore and purge take the row out at
  // once and put it back when refused (useTenants); they throw, and the page
  // reports the refusal.
  const {
    tenants: trashedTenants,
    loading,
    error,
    fetchedAt,
    restoreTenant,
    purgeTenant,
  } = useTenants("trashed");
  const [busyId, setBusyId] = useState<string | null>(null);
  // The action awaiting the operator's confirmation. Both restore and purge
  // are asked first: either one changes which tenants exist.
  const [pending, setPending] = useState<{
    kind: "restore" | "purge";
    tenant: Tenant;
  } | null>(null);
  // What the purge awaiting confirmation would remove and what would refuse
  // it, read while its confirmation is open. While it loads the confirmation
  // lists the kinds a purge removes and waits; if it cannot be read, it
  // lists them and lets the purge go ahead, whose own refusal still lands
  // under the row.
  const preview = usePurgePreview(
    pending?.kind === "purge" ? pending.tenant.tenantId : undefined,
  );
  const blockers = preview.data ? whatBlocksPurge(preview.data.blockers) : null;
  // Why the last purge of a tenant was refused, by tenant id.
  const [purgeRefusals, setPurgeRefusals] = useState<Record<string, string>>(
    {},
  );
  const { showNotification } = useNotification();

  const trashed = useMemo(
    () => [...trashedTenants].sort(byDeletedAt),
    [trashedTenants],
  );
  // Ages are read against the moment the list was fetched, so a row does not
  // change under the operator between refreshes.
  const now = useMemo(() => new Date(fetchedAt || Date.now()), [fetchedAt]);
  const pastRetention = useMemo(
    () =>
      trashed.filter(
        (t) => trashAge(t.deletedAt, t.purgeEligibleAt, now)?.pastRetention,
      ).length,
    [trashed, now],
  );
  const windowDays = useMemo(() => {
    const first = trashed[0];
    const info = first && trashAge(first.deletedAt, first.purgeEligibleAt, now);
    return info ? retentionDays(info) : null;
  }, [trashed, now]);
  const [retention, setRetention] = useTableFilter(
    RETENTION_FILTERS,
    DEFAULT_RETENTION_FILTER,
    PAGE_TABLE,
  );
  const inWindow = useMemo(
    () =>
      retention === DEFAULT_RETENTION_FILTER
        ? trashed
        : trashed.filter(
            (t) =>
              !!trashAge(t.deletedAt, t.purgeEligibleAt, now)?.pastRetention ===
              (retention === "past"),
          ),
    [trashed, retention, now],
  );
  const view = useTableView(inWindow, {
    text: TRASH_SEARCH,
    accessors: TRASH_SORT,
    url: PAGE_TABLE,
    tableId: TABLE_ID,
  });
  const sortProps = { current: view.sort, onSort: view.toggleSort };

  const handleRestore = async (t: Tenant) => {
    setBusyId(t.tenantId);
    try {
      const restored = await restoreTenant(t.tenantId);
      showNotification({
        type: "success",
        title: "Tenant restored",
        message: restored.displayName || restored.slug || restored.tenantId,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Restore failed",
        message: errorMessage(err),
      });
    } finally {
      setBusyId(null);
    }
  };

  const handlePurge = async (t: Tenant) => {
    setBusyId(t.tenantId);
    try {
      await purgeTenant(t.tenantId);
      setPurgeRefusals(({ [t.tenantId]: _, ...rest }) => rest);
      showNotification({
        type: "success",
        title: "Tenant purged",
        message: `${t.slug || t.tenantId} permanently removed`,
      });
    } catch (err) {
      setPurgeRefusals((r) => ({ ...r, [t.tenantId]: errorMessage(err) }));
    } finally {
      setBusyId(null);
    }
  };

  const empty = useMemo(
    () => trashed.length === 0 && !loading && !error,
    [trashed, loading, error],
  );

  return (
    <TableScope id={TABLE_ID} sort={view.sort}>
      <div className="space-y-6">
        <PageHeader
          title={
            <span className="flex items-center gap-2">
              Trash <HelpTip topic="tenantTrash" />
            </span>
          }
          description="Deleted tenants wait here until they are restored or purged. Nothing is purged automatically."
          showDefaultActions={false}
        />

        {error && (
          <Card>
            <CardContent className="py-6 text-center text-xs text-destructive">
              {error}
            </CardContent>
          </Card>
        )}

        {loading && (
          <div className="space-y-2">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        )}

        {empty && (
          <Card>
            <CardContent className="py-12 text-center">
              <ArchiveBoxIcon className="mx-auto size-10 text-muted-foreground/40" />
              <p className="mt-3 text-sm">Trash is empty.</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Deleted tenants land here. Restore returns them to active; purge
                removes physical rows immediately.
              </p>
            </CardContent>
          </Card>
        )}

        {trashed.length > 0 && (
          <div className="space-y-3">
            <p className={cn(T.helper, "tabular-nums")}>
              {trashed.length} {trashed.length === 1 ? "tenant" : "tenants"}
              {" · "}
              <span className={pastRetention > 0 ? "text-warning" : undefined}>
                {pastRetention} past retention
              </span>
              {windowDays !== null && <> · {windowDays}-day retention</>}
            </p>
            <TableToolbar
              query={view.query}
              onQueryChange={view.setQuery}
              searchLabel="Search the trash"
              placeholder="Search by name, slug or id…"
              filter={{
                label: "Retention",
                options: RETENTION_FILTERS,
                value: retention,
                onChange: setRetention,
              }}
              count={{ shown: view.rows.length, total: trashed.length }}
            />
            <Card className="p-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <SortableHead
                      label="Tenant"
                      column="tenant"
                      {...sortProps}
                    />
                    <SortableHead
                      label="Deleted"
                      column="deleted"
                      className="@md:w-40"
                      {...sortProps}
                    />
                    <SortableHead
                      label="Retention"
                      column="retention"
                      className="@md:w-56"
                      {...sortProps}
                    />
                    <TableHead className="@md:w-44">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {view.rows.length === 0 ? (
                    <TableRow>
                      <TableCell
                        colSpan={COLUMN_COUNT}
                        className="py-8 text-center text-muted-foreground"
                      >
                        No trashed tenant matches.
                      </TableCell>
                    </TableRow>
                  ) : null}
                  {view.rows.map((t) => {
                    const busy = busyId === t.tenantId;
                    const info = trashAge(t.deletedAt, t.purgeEligibleAt, now);
                    const refusal = purgeRefusals[t.tenantId];
                    const name = t.displayName || t.slug;
                    return (
                      <Fragment key={t.tenantId}>
                        <TableRow
                          className={refusal ? "border-b-0" : undefined}
                        >
                          <TableCell className="whitespace-normal">
                            <p className="font-medium">{name}</p>
                            {t.displayName && (
                              <div className="mt-0.5 flex items-center gap-1">
                                <span
                                  className={cn(
                                    T.codeSmall,
                                    "text-muted-foreground",
                                  )}
                                >
                                  {t.slug}
                                </span>
                                <IdentifierCopy
                                  iconOnly
                                  value={t.slug}
                                  label="Slug"
                                  className="p-1 [&_svg]:size-3"
                                />
                              </div>
                            )}
                          </TableCell>
                          <TableCell>
                            {info ? (
                              <div title={formatDateTime(info.deleted)}>
                                <p className="text-sm tabular-nums">
                                  {formatDate(info.deleted)}
                                </p>
                                <p className={T.hint}>
                                  {trashedAgo(info.days)}
                                </p>
                              </div>
                            ) : (
                              <span className={T.hint}>—</span>
                            )}
                          </TableCell>
                          <TableCell>
                            <TrashRetention
                              deletedAt={t.deletedAt}
                              purgeEligibleAt={t.purgeEligibleAt}
                              now={now}
                            />
                          </TableCell>
                          <TableCell>
                            <div className="flex justify-end gap-1.5">
                              <Button
                                size="sm"
                                variant="outline"
                                onClick={() =>
                                  setPending({ kind: "restore", tenant: t })
                                }
                                disabled={busy}
                              >
                                <ArrowUturnLeftIcon className="size-3.5" />
                                {busy ? "Restoring…" : "Restore"}
                              </Button>
                              <Button
                                size="sm"
                                variant="ghost"
                                className="text-destructive hover:bg-destructive/10"
                                onClick={() =>
                                  setPending({ kind: "purge", tenant: t })
                                }
                                disabled={busy}
                                aria-label={`Purge ${t.slug}`}
                                title="Purge permanently"
                              >
                                <TrashIcon className="size-3.5" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>
                        {refusal && (
                          <TableRow className="hover:bg-transparent">
                            <TableCell
                              colSpan={COLUMN_COUNT}
                              className="pt-0 whitespace-normal"
                            >
                              <p
                                role="alert"
                                className="flex items-start gap-2 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive"
                              >
                                <ExclamationTriangleIcon className="mt-px size-3.5 shrink-0" />
                                <span>
                                  <span className="font-medium">
                                    Purge refused.
                                  </span>{" "}
                                  {refusal}
                                </span>
                              </p>
                            </TableCell>
                          </TableRow>
                        )}
                      </Fragment>
                    );
                  })}
                </TableBody>
              </Table>
            </Card>
          </div>
        )}

        <ConfirmModal
          isOpen={pending?.kind === "restore"}
          onClose={() => setPending(null)}
          onConfirm={() =>
            pending ? handleRestore(pending.tenant) : undefined
          }
          loading={busyId !== null}
          type="warning"
          title="Restore this tenant?"
          message={
            <p>
              <span className="font-mono text-foreground">
                {pending?.tenant.slug || pending?.tenant.tenantId}
              </span>{" "}
              returns to the active tenants and leaves the trash.
            </p>
          }
          warning="If another tenant has taken this slug since, the restore is refused: rename one of them first."
          confirmText="Restore tenant"
        />

        {/* Purge is the one delete of a tenant nothing undoes, and it takes
            the CASCADE column of docs/deletion-semantics.md with it, so it
            asks for the slug and names what goes. Restore, above, puts
            things back and asks for nothing typed. */}
        <ConfirmModal
          isOpen={pending?.kind === "purge"}
          onClose={() => setPending(null)}
          onConfirm={() => (pending ? handlePurge(pending.tenant) : undefined)}
          loading={busyId !== null}
          type="danger"
          typeToConfirm={pending?.tenant.slug || pending?.tenant.tenantId}
          affected={
            preview.data ? affectedByPurge(preview.data) : PURGE_CASCADE
          }
          confirmDisabled={preview.isPending || blockers !== null}
          title="Purge this tenant?"
          message={
            <p>
              This permanently deletes{" "}
              <span className="font-mono text-foreground">
                {pending?.tenant.slug || pending?.tenant.tenantId}
              </span>{" "}
              from the database. The action cannot be undone.
            </p>
          }
          warning={
            <>
              {blockers && <p className="font-medium">{blockers}</p>}
              {preview.isError && (
                <p>
                  What it removes could not be read (
                  {errorMessage(preview.error)}); the kinds below are what any
                  purge removes.
                </p>
              )}
              <p>
                Physical S3 objects under{" "}
                <span className={T.code}>{"<bucket>/<tenant_id>/…"}</span> are
                not removed — clean them up separately if needed.
              </p>
            </>
          }
          confirmText="Purge permanently"
        />
      </div>
    </TableScope>
  );
}
