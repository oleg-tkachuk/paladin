"use client";

// /trash — recovery view for soft-deleted tenants. Migration 036 added
// the `deleted_at` column; TenantService gained RestoreTenant and
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
// The retention TTL reaper for unattended purge lives in worker/
// housekeeping.go and is BACKLOG'd separately.

import React, { useEffect, useMemo, useState } from "react";
import { ConnectError } from "@connectrpc/connect";
import {
  ArchiveBoxIcon,
  ArrowUturnLeftIcon,
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
import { RelativeTime } from "@/components/RelativeTime";
import { IdentityField } from "@/components/IdentityField";
import { T } from "@/lib/ui/typography";

export default function TrashPage() {
  const { fetchTenants, restoreTenant, purgeTenant } = useTenants();
  const [trashed, setTrashed] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [purgeTarget, setPurgeTarget] = useState<Tenant | null>(null);
  const { showNotification } = useNotification();

  const reload = async () => {
    setLoading(true);
    setError(null);
    try {
      // only_trashed=true → exclusively soft-deleted rows. The hook
      // shares state with the active list, so we read the result of
      // fetchTenants directly instead of relying on the hook's
      // internal `tenants` (which would conflict with the /tenants
      // page when both are mounted in the same tab session).
      const res = await fetchTenants("", "", { onlyTrashed: true });
      setTrashed(res.tenants);
    } catch (err) {
      setError(err instanceof ConnectError ? err.rawMessage : "Failed to load");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleRestore = async (t: Tenant) => {
    setBusyId(t.tenantId);
    try {
      const restored = await restoreTenant(t.tenantId);
      setTrashed((prev) => prev.filter((x) => x.tenantId !== t.tenantId));
      showNotification({
        type: "success",
        title: "Tenant restored",
        message: restored.displayName || restored.slug || restored.tenantId,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Restore failed",
        message:
          err instanceof ConnectError
            ? err.rawMessage
            : err instanceof Error
              ? err.message
              : String(err),
      });
    } finally {
      setBusyId(null);
    }
  };

  const handlePurge = async () => {
    if (!purgeTarget) return;
    const t = purgeTarget;
    setBusyId(t.tenantId);
    try {
      await purgeTenant(t.tenantId);
      setTrashed((prev) => prev.filter((x) => x.tenantId !== t.tenantId));
      showNotification({
        type: "success",
        title: "Tenant purged",
        message: `${t.slug || t.tenantId} permanently removed`,
      });
      setPurgeTarget(null);
    } catch (err) {
      showNotification({
        type: "error",
        title: "Purge failed",
        message:
          err instanceof ConnectError
            ? err.rawMessage
            : err instanceof Error
              ? err.message
              : String(err),
      });
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
        description="Soft-deleted tenants — recoverable until purged."
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
        <Card className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Tenant</TableHead>
                <TableHead className="hidden md:table-cell">Trashed</TableHead>
                <TableHead className="w-[200px] text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {trashed.map((t) => {
                const busy = busyId === t.tenantId;
                return (
                  <TableRow key={t.tenantId}>
                    <TableCell>
                      <div className="space-y-1">
                        <p className="font-medium">{t.displayName || t.slug}</p>
                        <IdentityField
                          label="slug"
                          value={t.slug}
                          immutable
                          labelWidth="w-12"
                          className={T.helper}
                        />
                      </div>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <RelativeTime ts={t.deletedAt || t.updatedAt} />
                    </TableCell>
                    <TableCell className="text-right">
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
                          aria-label="Purge tenant"
                        >
                          <TrashIcon className="size-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
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
