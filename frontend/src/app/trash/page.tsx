"use client";

// /trash — recovery view for soft-deleted resources. Migration 036
// adds `deleted_at` to tenants; the actual restore RPC is on the
// BACKLOG (handler needs to write `deleted_at = NULL` + Cedar
// re-evaluate). This page is the operator-facing affordance —
// scaffolds the layout so the moment the RPCs land, wiring is a
// one-liner.
//
// Today the page lists tenants with `deleted_at != NULL`. It calls
// the existing ListTenants RPC with a CEL filter; when the backend
// adds a `include_trashed` flag the filter switches to that.

import React, { useEffect, useMemo, useState } from "react";
import { ConnectError } from "@connectrpc/connect";
import {
  ArchiveBoxIcon,
  ArrowUturnLeftIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { tenantClient } from "@/lib/connect/client";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";

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
import { useNotification } from "@/components/ui/Notification";
import { RelativeTime } from "@/components/RelativeTime";
import { IdentityField } from "@/components/IdentityField";
import { T } from "@/lib/ui/typography";

// Until backend `include_trashed` flag lands, we have no canonical
// way to fetch soft-deleted tenants — ListTenants filters them out by
// design. This page renders the empty state with the right CTA copy
// so operators know where to look later. Backlog: wire actual fetch
// + restore RPC.
const RESTORE_AVAILABLE = false;

export default function TrashPage() {
  const [trashed, setTrashed] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { showNotification } = useNotification();

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    (async () => {
      try {
        // Aspirational filter: backend may not honour it yet — the
        // page degrades to an empty list rather than failing.
        const res = await tenantClient.listTenants({
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
          filter: "deleted_at != null",
        });
        if (cancelled) return;
        setTrashed(res.tenants);
      } catch (err) {
        if (cancelled) return;
        // Non-fatal: filter not supported yet → empty page.
        setError(
          err instanceof ConnectError ? err.rawMessage : "Failed to load",
        );
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const handleRestore = (t: Tenant) => {
    if (!RESTORE_AVAILABLE) {
      showNotification({
        type: "error",
        title: "Restore not yet available",
        message:
          "TenantService.RestoreTenant RPC is BACKLOG'd. Use direct DB intervention until it lands.",
      });
      return;
    }
    // When wired: tenantClient.restoreTenant({ name: ... })
    console.log("restore", t.tenantId);
  };

  const handlePurge = (t: Tenant) => {
    if (!RESTORE_AVAILABLE) {
      showNotification({
        type: "error",
        title: "Purge not yet available",
        message:
          "Hard-delete reaper lives in worker/housekeeping.go and runs on a TTL; manual purge RPC is BACKLOG'd.",
      });
      return;
    }
    console.log("purge", t.tenantId);
  };

  const empty = useMemo(
    () => trashed.length === 0 && !loading,
    [trashed, loading],
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title="Trash"
        description="Soft-deleted tenants — recoverable within the retention window."
        showDefaultActions={false}
      />

      {error && (
        <Card>
          <CardContent className="py-6 text-center text-xs text-muted-foreground">
            <p>
              Soft-delete listing requires a backend filter that isn&apos;t live
              yet.
            </p>
            <p className="mt-1 font-mono text-[10px]">{error}</p>
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

      {empty && !error && (
        <Card>
          <CardContent className="py-12 text-center">
            <ArchiveBoxIcon className="mx-auto size-10 text-muted-foreground/40" />
            <p className="mt-3 text-sm">Trash is empty.</p>
            <p className="mt-1 text-xs text-muted-foreground">
              Deleted tenants land here. Restore returns them to active; purge
              removes physical rows after the retention TTL.
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
                <TableHead className="hidden md:table-cell">Deleted</TableHead>
                <TableHead className="w-[160px] text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {trashed.map((t) => (
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
                    <RelativeTime ts={t.updatedAt} />
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-1.5">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => handleRestore(t)}
                      >
                        <ArrowUturnLeftIcon className="size-3.5" />
                        Restore
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-destructive hover:bg-destructive/10"
                        onClick={() => handlePurge(t)}
                      >
                        <TrashIcon className="size-3.5" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
    </div>
  );
}
