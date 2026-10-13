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
//   - irreversible — confirmed via AlertDialog
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

import { Fragment, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
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
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
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

export default function TrashPage() {
  // `error` is the hook's query-error state (set by fetchTenants on failure);
  // the contract makes fetchTenants state-only, so we read it here instead of
  // catching a throw. restoreTenant/purgeTenant are mutations (throw-only) and
  // surface via toasts below.
  const { fetchTenants, restoreTenant, purgeTenant, error } = useTenants();
  const [busyId, setBusyId] = useState<string | null>(null);
  const [purgeTarget, setPurgeTarget] = useState<Tenant | null>(null);
  // Why the last purge of a tenant was refused, by tenant id.
  const [purgeRefusals, setPurgeRefusals] = useState<Record<string, string>>(
    {},
  );
  const { showNotification } = useNotification();

  // only_trashed=true → exclusively soft-deleted rows. We read the RETURN
  // value of fetchTenants rather than the hook's shared `tenants` (which
  // would conflict with the /tenants page when both are mounted in the same
  // tab session). fetchTenants is a query: on failure it sets the hook's
  // `error` (rendered in the banner) and returns an empty page — no throw.
  const trashQuery = useQuery({
    queryKey: ["trashedTenants"],
    queryFn: () =>
      fetchTenants("", "", { onlyTrashed: true }).then((res) => res.tenants),
  });
  const trashed = useMemo(
    () => [...(trashQuery.data ?? [])].sort(byDeletedAt),
    [trashQuery.data],
  );
  // Ages are read against the moment the list was fetched, so a row does not
  // change under the operator between refreshes.
  const fetchedAt = trashQuery.dataUpdatedAt;
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
  const loading = trashQuery.isFetching;
  // Mutations refetch instead of optimistically splicing local state.
  const reload = () => void trashQuery.refetch();

  const handleRestore = async (t: Tenant) => {
    setBusyId(t.tenantId);
    try {
      const restored = await restoreTenant(t.tenantId);
      reload();
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

  const handlePurge = async () => {
    if (!purgeTarget) return;
    const t = purgeTarget;
    setBusyId(t.tenantId);
    setPurgeTarget(null);
    try {
      await purgeTenant(t.tenantId);
      setPurgeRefusals(({ [t.tenantId]: _, ...rest }) => rest);
      reload();
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
    <div className="space-y-6">
      <PageHeader
        title="Trash"
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
            <span className={pastRetention > 0 ? "text-chart-3" : undefined}>
              {pastRetention} past retention
            </span>
            {windowDays !== null && <> · {windowDays}-day retention</>}
          </p>
          <Card className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Tenant</TableHead>
                  <TableHead className="@md:w-40">Deleted</TableHead>
                  <TableHead className="@md:w-56">Retention</TableHead>
                  <TableHead className="@md:w-44">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {trashed.map((t) => {
                  const busy = busyId === t.tenantId;
                  const info = trashAge(t.deletedAt, t.purgeEligibleAt, now);
                  const refusal = purgeRefusals[t.tenantId];
                  const name = t.displayName || t.slug;
                  return (
                    <Fragment key={t.tenantId}>
                      <TableRow className={refusal ? "border-b-0" : undefined}>
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
                              <p className={T.hint}>{trashedAgo(info.days)}</p>
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
                              onClick={() => void handleRestore(t)}
                              disabled={busy}
                            >
                              <ArrowUturnLeftIcon className="size-3.5" />
                              {busy ? "Restoring…" : "Restore"}
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              className="text-destructive hover:bg-destructive/10"
                              onClick={() => setPurgeTarget(t)}
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

      <AlertDialog
        open={!!purgeTarget}
        onOpenChange={(o) => !o && setPurgeTarget(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Purge this tenant?</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-2">
                <p>
                  This permanently deletes{" "}
                  <span className="font-mono text-foreground">
                    {purgeTarget?.slug || purgeTarget?.tenantId}
                  </span>{" "}
                  from the database. The action cannot be undone.
                </p>
                <p className="text-xs text-muted-foreground">
                  Physical S3 objects under{" "}
                  <span className={T.code}>{"<bucket>/<tenant_id>/…"}</span> are
                  NOT removed — clean them up separately if needed.
                </p>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={handlePurge}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              Purge permanently
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
