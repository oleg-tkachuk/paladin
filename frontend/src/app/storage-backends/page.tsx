"use client";

// /storage-backends — platform-admin index of registered S3-compatible
// backends. Buckets FK into this table; Tenants pick one of these
// (paired with a Bucket) at creation as their default binding. Without
// at least one backend row the rest of the platform can't provision
// anything, so this is the first thing an operator hits on a fresh
// install.
//
// Scope: list + create. Edit / delete / RotateCredentials / TestBackend
// live behind a row dropdown when needed; the create form is the
// 80% case for this page.

import React, { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import {
  ArrowPathIcon,
  CloudIcon,
  MagnifyingGlassIcon,
  PlusIcon,
  ServerStackIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useBackends } from "@/hooks/useBackends";
import { BackendRegisterDialog } from "./BackendRegisterDialog";
import { STORAGE_KIND_LABELS } from "@/lib/storageKind";
import { useNotification } from "@/components/ui/Notification";
import { ListLoadError } from "@/components/ui/ListLoadError";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card } from "@/components/ui/Card";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/Skeleton";
import { cn } from "@/lib/utils";
import { searchFilter } from "@/lib/cel";
import { providerLabel } from "@/lib/storageProvider";
import { compatibilityBadge, featureWarnings } from "@/lib/storageFeatures";
import { T } from "@/lib/ui/typography";
import { errorMessage } from "@/hooks/errorContract";

export default function StorageBackendsPage() {
  const {
    backends,
    loading,
    fetchBackends,
    createBackend,
    setBackendEnabled,
    setBackendReadOnly,
    setBackendMaintenance,
    error: listError,
  } = useBackends();
  const { showNotification } = useNotification();

  // Server-side, like /buckets: the page used to fetch every backend and
  // narrow the array in the browser. The derived `search` field joins the same
  // four columns this box used to match — id, display name, region, endpoint —
  // so nothing it used to find has stopped being findable.
  const [search, setSearch] = useState("");
  // Trails `search` by 300ms so a refetch does not fire per keystroke.
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const [drainingId, setDrainingId] = useState<string | null>(null);
  const [maintainingId, setMaintainingId] = useState<string | null>(null);
  // Multi-select for bulk actions (client-side fan-out over the per-backend
  // OCC-guarded RPCs — each backend carries its own resource_version).
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);

  // handleToggle flips a backend's enabled state. The server refuses to
  // disable the configured default backend (FailedPrecondition) and a
  // stale resource_version (Aborted) — both surface as a clear toast
  // rather than a raw error.
  const handleToggle = async (
    backendId: string,
    nextEnabled: boolean,
    resourceVersion: string,
  ) => {
    try {
      setTogglingId(backendId);
      await setBackendEnabled(backendId, nextEnabled, resourceVersion);
      showNotification({
        type: "success",
        title: nextEnabled ? "Backend enabled" : "Backend disabled",
        message: backendId,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Could not change backend state",
        message: errorMessage(err),
      });
    } finally {
      setTogglingId(null);
    }
  };

  // handleDrain flips a backend's read-only "drain" state
  // (`001_initial_schema.sql`).
  // Draining keeps reads working while refusing mutations, so an operator can
  // migrate data off before disabling. OCC-guarded like enable/disable.
  const handleDrain = async (
    backendId: string,
    nextReadOnly: boolean,
    resourceVersion: string,
  ) => {
    try {
      setDrainingId(backendId);
      await setBackendReadOnly(backendId, nextReadOnly, resourceVersion);
      showNotification({
        type: "success",
        title: nextReadOnly
          ? "Backend draining (read-only)"
          : "Backend writable",
        message: backendId,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Could not change drain state",
        message: errorMessage(err),
      });
    } finally {
      setDrainingId(null);
    }
  };

  // handleMaintenance raises/clears the operator-set maintenance flag
  // (`001_initial_schema.sql`) — an advisory label, OCC-guarded like enable/drain.
  const handleMaintenance = async (
    backendId: string,
    nextMaintenance: boolean,
    resourceVersion: string,
  ) => {
    try {
      setMaintainingId(backendId);
      await setBackendMaintenance(backendId, nextMaintenance, resourceVersion);
      showNotification({
        type: "success",
        title: nextMaintenance
          ? "Backend flagged for maintenance"
          : "Maintenance flag cleared",
        message: backendId,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Could not change maintenance flag",
        message: errorMessage(err),
      });
    } finally {
      setMaintainingId(null);
    }
  };

  // ── bulk actions (multi-select → client-side fan-out) ────────────────────
  type BulkAction =
    | "enable"
    | "disable"
    | "drain"
    | "undrain"
    | "maintenance"
    | "unmaintenance";

  const toggleSelect = (id: string, on: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

  const clearSelection = () => setSelected(new Set());

  // applies reports whether an action changes a given backend's state (so a
  // no-op is skipped rather than firing a pointless RPC). Enable/disable gate
  // on `enabled`; drain/undrain only apply to an ENABLED backend (a disabled
  // one already rejects everything, so draining it is meaningless).
  const applies = (
    b: (typeof backends)[number],
    action: BulkAction,
  ): boolean => {
    switch (action) {
      case "enable":
        return !b.enabled;
      case "disable":
        return b.enabled;
      case "drain":
        return b.enabled && !b.readOnly;
      case "undrain":
        return b.enabled && b.readOnly;
      case "maintenance":
        return !b.maintenance;
      case "unmaintenance":
        return b.maintenance;
    }
  };

  const runBulk = async (action: BulkAction) => {
    const targets = backends.filter((b) => selected.has(b.backendId));
    const applicable = targets.filter((b) => applies(b, action));
    if (applicable.length === 0) {
      showNotification({
        type: "success",
        title: "Nothing to do",
        message: "No selected backend is in a state this action changes.",
      });
      return;
    }
    setBulkBusy(true);
    // Fan out over the existing OCC-guarded per-backend RPCs. allSettled so one
    // rejection (e.g. disabling the default backend → FailedPrecondition)
    // doesn't abort the rest; failures are reported per backend.
    const results = await Promise.allSettled(
      applicable.map((b) => {
        switch (action) {
          case "enable":
            return setBackendEnabled(b.backendId, true, b.resourceVersion);
          case "disable":
            return setBackendEnabled(b.backendId, false, b.resourceVersion);
          case "drain":
            return setBackendReadOnly(b.backendId, true, b.resourceVersion);
          case "undrain":
            return setBackendReadOnly(b.backendId, false, b.resourceVersion);
          case "maintenance":
            return setBackendMaintenance(b.backendId, true, b.resourceVersion);
          case "unmaintenance":
            return setBackendMaintenance(b.backendId, false, b.resourceVersion);
        }
      }),
    );
    const failedIds = applicable
      .filter((_, i) => results[i].status === "rejected")
      .map((b) => b.backendId);
    const ok = results.length - failedIds.length;
    const skipped = targets.length - applicable.length;
    showNotification({
      type: failedIds.length > 0 ? "error" : "success",
      title:
        `Bulk ${action}: ${ok} ok` +
        (failedIds.length ? `, ${failedIds.length} failed` : "") +
        (skipped ? `, ${skipped} skipped` : ""),
      message: failedIds.length ? failedIds.join(", ") : undefined,
    });
    setBulkBusy(false);
    clearSelection();
    void refetch();
  };

  const [createOpen, setCreateOpen] = useState(false);
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]);

  // One place that knows how to ask, so the Refresh button and the post-bulk
  // reload cannot drift into fetching something other than what is on screen.
  const refetch = useCallback(
    () => fetchBackends(searchFilter(debouncedSearch)),
    [fetchBackends, debouncedSearch],
  );

  useEffect(() => {
    void refetch();
  }, [refetch]);

  // No client-side narrowing left. Re-adding it would be dead code that
  // quietly disagreed with the API the day the two definitions of "matches"
  // drifted apart.
  const filtered = backends;

  const hasSearch = search.trim() !== "";
  // The debounce has not fired yet, or its refetch is still running.
  const searchPending = search.trim() !== debouncedSearch.trim() || loading;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Storage Backends"
        description="Physical S3-compatible targets. Buckets and tenants bind here."
        showDefaultActions={false}
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <PlusIcon className="size-4" />
            New backend
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-60">
          <MagnifyingGlassIcon className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="search"
            placeholder="Search by ID, name, region, endpoint…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <Button
          variant="outline"
          size="icon"
          onClick={() => void refetch()}
          aria-label="Refresh"
        >
          <ArrowPathIcon className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>

      {/* Bulk-action bar — shown once ≥1 backend is selected. Each action
          fans out over the selection via the per-backend OCC-guarded RPCs and
          skips no-ops (see runBulk). */}
      {selected.size > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2 text-sm">
          <span className="font-medium">{selected.size} selected</span>
          <div className="flex flex-wrap items-center gap-1.5">
            <Button
              size="sm"
              variant="outline"
              aria-label="Bulk enable"
              disabled={bulkBusy}
              onClick={() => void runBulk("enable")}
            >
              Enable
            </Button>
            <Button
              size="sm"
              variant="outline"
              aria-label="Bulk disable"
              disabled={bulkBusy}
              onClick={() => void runBulk("disable")}
            >
              Disable
            </Button>
            <Button
              size="sm"
              variant="outline"
              aria-label="Bulk drain"
              disabled={bulkBusy}
              onClick={() => void runBulk("drain")}
            >
              Drain
            </Button>
            <Button
              size="sm"
              variant="outline"
              aria-label="Bulk undrain"
              disabled={bulkBusy}
              onClick={() => void runBulk("undrain")}
            >
              Undrain
            </Button>
            <Button
              size="sm"
              variant="outline"
              aria-label="Bulk maintenance"
              disabled={bulkBusy}
              onClick={() => void runBulk("maintenance")}
            >
              Maintenance
            </Button>
            <Button
              size="sm"
              variant="outline"
              aria-label="Bulk clear maintenance"
              disabled={bulkBusy}
              onClick={() => void runBulk("unmaintenance")}
            >
              Clear maint.
            </Button>
          </div>
          <Button
            size="sm"
            variant="ghost"
            disabled={bulkBusy}
            onClick={clearSelection}
          >
            Clear
          </Button>
        </div>
      )}

      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10">
                <Checkbox
                  aria-label="Select all backends"
                  checked={
                    filtered.length > 0 &&
                    filtered.every((b) => selected.has(b.backendId))
                  }
                  onCheckedChange={(v) =>
                    setSelected(
                      v === true
                        ? new Set(filtered.map((b) => b.backendId))
                        : new Set(),
                    )
                  }
                />
              </TableHead>
              <TableHead className="w-55">Backend ID</TableHead>
              <TableHead>Display name</TableHead>
              <TableHead className="hidden md:table-cell">Kind</TableHead>
              <TableHead className="hidden md:table-cell">Type</TableHead>
              <TableHead className="hidden md:table-cell">Region</TableHead>
              <TableHead className="hidden lg:table-cell">Endpoint</TableHead>
              <TableHead className="w-40 text-right">Status</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && backends.length === 0 ? (
              [0, 1, 2].map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={8} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : listError ? (
              // "No storage backends registered yet" for a list that FAILED is
              // the same lie /buckets already refuses to tell, with a worse
              // ending: it invites the operator to register a backend that
              // exists, and a second registration points a second row at the
              // same store. The hook exposed `error` all along; this page took
              // every other field from it.
              <TableRow>
                <TableCell colSpan={8} className="h-48 text-center">
                  <ListLoadError
                    what="Storage backends"
                    reason={listError}
                    onRetry={() => void refetch()}
                  />
                </TableCell>
              </TableRow>
            ) : filtered.length === 0 ? (
              <TableRow>
                <TableCell colSpan={8} className="h-48 text-center">
                  <div className="flex flex-col items-center gap-3 text-muted-foreground">
                    <CloudIcon className="size-10 opacity-40" />
                    <p className="text-sm">
                      {/*
                        Three states, not two: the debounce has not fired, the
                        request is in flight, or the server found nothing.
                        Collapsing the first two into "No backends match" tells
                        an operator their backend is gone while they type.
                      */}
                      {searchPending
                        ? "Searching…"
                        : hasSearch
                          ? "No backends match your search."
                          : "No storage backends registered yet."}
                    </p>
                    {!searchPending && !hasSearch && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setCreateOpen(true)}
                      >
                        <PlusIcon className="size-4" />
                        Register the first backend
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              filtered.map((b) => {
                const detailHref = `/storage-backends/${encodeURIComponent(b.backendId)}`;
                return (
                  <TableRow key={b.backendId} className="group">
                    <TableCell className="w-10">
                      <Checkbox
                        aria-label={`Select ${b.backendId}`}
                        checked={selected.has(b.backendId)}
                        onCheckedChange={(v) =>
                          toggleSelect(b.backendId, v === true)
                        }
                      />
                    </TableCell>
                    <TableCell>
                      <Link
                        href={detailHref}
                        className="flex items-center gap-3 hover:text-primary"
                      >
                        <div className="flex size-8 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
                          <ServerStackIcon className="size-4" />
                        </div>
                        <span className="font-medium group-hover:underline">
                          {b.backendId}
                        </span>
                      </Link>
                    </TableCell>
                    <TableCell>
                      <Link href={detailHref} className="hover:text-primary">
                        {b.displayName || (
                          <span className="text-muted-foreground italic">
                            (unnamed)
                          </span>
                        )}
                      </Link>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <Badge variant="outline" className={T.labelTight}>
                        {STORAGE_KIND_LABELS[b.kind] || "—"}
                      </Badge>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      {(() => {
                        const p = providerLabel(b);
                        if (p.label === "—")
                          return (
                            <span className="text-muted-foreground">—</span>
                          );
                        return (
                          <Badge
                            variant="secondary"
                            className={T.labelTight}
                            title={
                              p.derived
                                ? "Inferred from the endpoint (no provider set)"
                                : undefined
                            }
                          >
                            {p.label}
                            {p.derived && (
                              <span className="ml-1 opacity-60">?</span>
                            )}
                          </Badge>
                        );
                      })()}
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <span className="font-mono text-xs text-muted-foreground">
                        {b.region || "—"}
                      </span>
                    </TableCell>
                    <TableCell className="hidden lg:table-cell">
                      <span
                        className="font-mono text-caption text-muted-foreground truncate"
                        title={b.endpoint}
                      >
                        {b.endpoint || "—"}
                      </span>
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-2">
                        <Badge
                          variant={b.enabled ? "outline" : "destructive"}
                          className={T.labelTight}
                        >
                          {b.enabled ? "Enabled" : "Disabled"}
                        </Badge>
                        {b.enabled && b.readOnly && (
                          <Badge variant="secondary" className={T.labelTight}>
                            Draining
                          </Badge>
                        )}
                        {b.maintenance && (
                          <Badge variant="secondary" className={T.labelTight}>
                            Maintenance
                          </Badge>
                        )}
                        {/* Derived health from the last TestBackend probe
                            (001_initial_schema.sql). Advisory — does not gate ops. */}
                        {b.healthStatus === "error" ? (
                          <Badge
                            variant="destructive"
                            className={T.labelTight}
                            title={b.healthMessage || "Last probe failed"}
                          >
                            Probe failed
                          </Badge>
                        ) : b.healthStatus === "ok" ? (
                          <Badge variant="outline" className={T.labelTight}>
                            Healthy
                          </Badge>
                        ) : (
                          <Badge
                            variant="secondary"
                            className={T.labelTight}
                            title="Run Test to probe connectivity"
                          >
                            Untested
                          </Badge>
                        )}
                        {/* What the last probe found for the S3 features
                            Paladin uses (ADR-0026); the detail page lists
                            them. */}
                        {(() => {
                          const c = compatibilityBadge(b);
                          const why = featureWarnings(b)
                            .map((w) => w.text)
                            .join("\n");
                          return (
                            <Badge
                              variant={c.variant}
                              className={T.labelTight}
                              title={why || undefined}
                            >
                              {c.label}
                            </Badge>
                          );
                        })()}
                        {/* Drain toggle — only meaningful on an enabled
                            backend (a disabled one already rejects all ops). */}
                        {b.enabled && (
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={drainingId === b.backendId}
                            onClick={() =>
                              void handleDrain(
                                b.backendId,
                                !b.readOnly,
                                b.resourceVersion,
                              )
                            }
                          >
                            {drainingId === b.backendId
                              ? "…"
                              : b.readOnly
                                ? "Undrain"
                                : "Drain"}
                          </Button>
                        )}
                        {/* Maintenance is advisory + orthogonal — settable on
                            any backend regardless of enabled/drain state. */}
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={
                            b.maintenance
                              ? `Clear maintenance on ${b.backendId}`
                              : `Flag ${b.backendId} for maintenance`
                          }
                          disabled={maintainingId === b.backendId}
                          onClick={() =>
                            void handleMaintenance(
                              b.backendId,
                              !b.maintenance,
                              b.resourceVersion,
                            )
                          }
                        >
                          {maintainingId === b.backendId
                            ? "…"
                            : b.maintenance
                              ? "Clear maint."
                              : "Maintain"}
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={togglingId === b.backendId}
                          onClick={() =>
                            void handleToggle(
                              b.backendId,
                              !b.enabled,
                              b.resourceVersion,
                            )
                          }
                        >
                          {togglingId === b.backendId
                            ? "…"
                            : b.enabled
                              ? "Disable"
                              : "Enable"}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      <BackendRegisterDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        createBackend={createBackend}
      />
    </div>
  );
}
