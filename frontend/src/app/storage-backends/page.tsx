"use client";

// /storage-backends — platform-admin index of registered S3-compatible
// backends. Buckets FK into this table; Tenants pick one of these
// (paired with a Bucket) at creation as their default binding. Without
// at least one backend row the rest of the platform can't provision
// anything, so this is the first thing an operator hits on a fresh
// install.
//
// One row per backend, one line per row: who it is, where it lives, the
// state it is in, what the last probe found. Every state change sits behind
// the row's menu or the bulk bar, and disabling asks first.

import { usePageAction } from "@/context/ActionsContext";
import { CREATE_ACTION } from "@/lib/paletteActions";
import { ListBody, ListPage } from "@/components/layout/templates/ListPage";
import { emptyReason } from "@/lib/emptyState";
import React, { Fragment, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  ArrowPathIcon,
  ArrowTopRightOnSquareIcon,
  CloudIcon,
  EllipsisHorizontalIcon,
  NoSymbolIcon,
  PauseCircleIcon,
  PlayCircleIcon,
  PlusIcon,
  ServerStackIcon,
  WrenchScrewdriverIcon,
} from "@heroicons/react/24/outline";

import { PageHeader } from "@/components/layout/PageHeader";
import { useBackends } from "@/hooks/useBackends";
import { BackendRegisterDialog } from "./BackendRegisterDialog";
import { STORAGE_KIND_LABELS } from "@/lib/storageKind";
import { useNotification } from "@/components/ui/Notification";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { SortableHead } from "@/components/ui/SortHeader";
import { useTableSort } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { sortRows, type SortAccessors } from "@/lib/table/view";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { SelectionBar } from "@/components/ui/SelectionBar";
import { Separator } from "@/components/ui/separator";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  SelectContent,
  SelectItem,
  SelectRoot,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/Select";
import {
  Table,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { searchFilter } from "@/lib/cel";
import {
  GUESSED_PROVIDER_HINT,
  GUESSED_PROVIDER_LABEL,
  providerLabel,
} from "@/lib/storageProvider";
import {
  COMPATIBLE_VARIANT,
  compatibilityBadge,
  featureWarnings,
} from "@/lib/storageFeatures";
import { errorMessage } from "@/hooks/errorContract";
import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import {
  STATE_FILTERS,
  STATE_FILTER_LABELS,
  allOf,
  healthView,
  lifecycleState,
  stateFilter,
  type HealthTone,
  type StateFilter,
} from "./backendView";
import { TableScope } from "@/components/ui/TableScope";

/** This list's id in the user's table preferences. */
const TABLE_ID = "storageBackends";

// Checkbox, backend, storage, region, state, health, menu.
const COLUMN_COUNT = 7;
// Trails the search box so a refetch does not fire per keystroke.
const SEARCH_DEBOUNCE_MS = 300;

const HEALTH_DOT: Record<HealthTone, string> = {
  ok: "bg-chart-2",
  error: "bg-destructive",
  unknown: "bg-muted-foreground/50",
};

type Action =
  "enable" | "disable" | "drain" | "undrain" | "maintenance" | "unmaintenance";

// What each action is called, and the toast that reports it.
const ACTION_TEXT: Record<
  Action,
  { bulkLabel: string; button: string; done: string; failed: string }
> = {
  enable: {
    bulkLabel: "Bulk enable",
    button: "Enable",
    done: "Backend enabled",
    failed: "Could not change backend state",
  },
  disable: {
    bulkLabel: "Bulk disable",
    button: "Disable",
    done: "Backend disabled",
    failed: "Could not change backend state",
  },
  drain: {
    bulkLabel: "Bulk drain",
    button: "Drain",
    done: "Backend draining (read-only)",
    failed: "Could not change drain state",
  },
  undrain: {
    bulkLabel: "Bulk undrain",
    button: "Undrain",
    done: "Backend writable",
    failed: "Could not change drain state",
  },
  maintenance: {
    bulkLabel: "Bulk maintenance",
    button: "Maintenance",
    done: "Backend flagged for maintenance",
    failed: "Could not change maintenance flag",
  },
  unmaintenance: {
    bulkLabel: "Bulk clear maintenance",
    button: "Clear maintenance",
    done: "Maintenance flag cleared",
    failed: "Could not change maintenance flag",
  },
};

// The bulk bar's pairs: each pair sets one flag on and off.
const BULK_GROUPS: readonly (readonly [Action, Action])[] = [
  ["enable", "disable"],
  ["drain", "undrain"],
  ["maintenance", "unmaintenance"],
];

// applies reports whether an action changes a given backend's state, so a
// no-op is skipped rather than firing a pointless RPC. Drain and undrain only
// apply to an ENABLED backend: a disabled one already rejects everything.
// Maintenance is advisory and orthogonal, settable in any state.
function applies(b: StorageBackend, action: Action): boolean {
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
}

// The disable confirmation: what stops, so the click is not a surprise.
const DISABLE_CONSEQUENCE =
  "Buckets on it stop serving reads and writes until it is enabled again. " +
  "The configured default backend cannot be disabled.";

// The list is searched on the server and loaded whole, so it sorts here.
const SORT_COLUMNS = [
  "backend",
  "storage",
  "region",
  "state",
  "health",
] as const;
type BackendColumn = (typeof SORT_COLUMNS)[number];
const BACKEND_SORT: SortAccessors<StorageBackend, BackendColumn> = {
  backend: (b) => b.backendId,
  storage: (b) => STORAGE_KIND_LABELS[b.kind] || undefined,
  region: (b) => b.region || undefined,
  state: (b) => lifecycleState(b).label,
  health: (b) => healthView(b).label,
};

export default function StorageBackendsPage() {
  // Server-side, like /buckets: the derived `search` field joins id, display
  // name, region and endpoint, and the state choice is a conjunct beside it.
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  const [debouncedSearch, setDebouncedSearch] = useState(search);
  const [state, setState] = useState<StateFilter>("all");
  const {
    backends,
    loading,
    refetch,
    createBackend,
    setBackendEnabled,
    setBackendReadOnly,
    setBackendMaintenance,
    error: listError,
  } = useBackends({
    filter: allOf(searchFilter(debouncedSearch), stateFilter(state)),
  });
  const { showNotification } = useNotification();
  const { sort, toggleSort } = useTableSort<BackendColumn>(undefined, {
    url: PAGE_TABLE,
    tableId: TABLE_ID,
    columns: SORT_COLUMNS,
  });
  const sortProps = { current: sort, onSort: toggleSort };
  const sorted = useMemo(
    () => sortRows(backends, sort, BACKEND_SORT),
    [backends, sort],
  );

  // The backend a row action is running on; its menu stays disabled meanwhile.
  const [busyId, setBusyId] = useState<string | null>(null);
  // Multi-select for bulk actions (client-side fan-out over the per-backend
  // OCC-guarded RPCs — each backend carries its own resource_version).
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  // Disabling asks first: one backend from a row menu, or the selection.
  const [confirmDisable, setConfirmDisable] = useState<
    StorageBackend | "selection" | null
  >(null);
  const [createOpen, setCreateOpen] = useState(false);
  // The palette offers the create while this page is on screen.
  usePageAction({
    id: CREATE_ACTION.storageBackend,
    label: "New backend",
    description: "An S3-compatible storage target",
    icon: PlusIcon,
    category: "create",
    perform: () => setCreateOpen(true),
  });

  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [search]);

  const call = (b: StorageBackend, action: Action) => {
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
  };

  // The server refuses to disable the configured default backend
  // (FailedPrecondition) and a stale resource_version (Aborted); both reach
  // the operator as a toast rather than a raw error.
  const runRow = async (b: StorageBackend, action: Action) => {
    setBusyId(b.backendId);
    try {
      await call(b, action);
      showNotification({
        type: "success",
        title: ACTION_TEXT[action].done,
        message: b.backendId,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: ACTION_TEXT[action].failed,
        message: errorMessage(err),
      });
    } finally {
      setBusyId(null);
    }
  };

  const clearSelection = () => setSelected(new Set());

  const toggleSelect = (id: string, on: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });

  const runBulk = async (action: Action) => {
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
    // allSettled so one rejection (disabling the default backend, say) does
    // not abort the rest; failures are reported per backend.
    const results = await Promise.allSettled(
      applicable.map((b) => call(b, action)),
    );
    const failedIds = applicable
      .filter((_, i) => results[i].status === "rejected")
      .map((b) => b.backendId);
    const ok = results.length - failedIds.length;
    const skipped = targets.length - applicable.length;
    showNotification({
      type: failedIds.length > 0 ? "error" : "success",
      title:
        `${ACTION_TEXT[action].button}: ${ok} ok` +
        (failedIds.length ? `, ${failedIds.length} failed` : "") +
        (skipped ? `, ${skipped} skipped` : ""),
      message: failedIds.length ? failedIds.join(", ") : undefined,
    });
    setBulkBusy(false);
    clearSelection();
    void refetch();
  };

  const startBulk = (action: Action) => {
    if (action === "disable") setConfirmDisable("selection");
    else void runBulk(action);
  };

  const confirmDisableNow = async () => {
    const target = confirmDisable;
    setConfirmDisable(null);
    if (target === "selection") await runBulk("disable");
    else if (target) await runRow(target, "disable");
  };

  const hasFilters = search.trim() !== "" || state !== "all";
  // The debounce has not fired yet, or its refetch is still running.
  const searchPending = search.trim() !== debouncedSearch.trim() || loading;
  const allSelected =
    backends.length > 0 && backends.every((b) => selected.has(b.backendId));

  return (
    <TableScope id={TABLE_ID} sort={sort}>
      <ListPage
        header={
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
        }
        toolbar={
          <TableToolbar
            query={search}
            onQueryChange={setSearch}
            searchLabel="Search storage backends"
            placeholder="Search by ID, name, region, endpoint…"
            actions={
              <>
                <Button
                  variant="outline"
                  size="icon"
                  onClick={() => void refetch()}
                  aria-label="Refresh"
                >
                  <ArrowPathIcon
                    className={cn("size-4", loading && "animate-spin")}
                  />
                </Button>
              </>
            }
          >
            <SelectRoot
              value={state}
              onValueChange={(v) => setState(v as StateFilter)}
            >
              <SelectTrigger aria-label="Filter by state" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STATE_FILTERS.map((s) => (
                  <SelectItem key={s} value={s}>
                    {STATE_FILTER_LABELS[s]}
                  </SelectItem>
                ))}
              </SelectContent>
            </SelectRoot>
          </TableToolbar>
        }
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10">
                <Checkbox
                  aria-label="Select all backends"
                  checked={allSelected}
                  onCheckedChange={(v) =>
                    setSelected(
                      v === true
                        ? new Set(backends.map((b) => b.backendId))
                        : new Set(),
                    )
                  }
                />
              </TableHead>
              <SortableHead label="Backend" column="backend" {...sortProps} />
              <SortableHead
                label="Storage"
                column="storage"
                className="hidden @2xl:table-cell"
                {...sortProps}
              />
              <SortableHead
                label="Region"
                column="region"
                className="hidden @4xl:table-cell"
                {...sortProps}
              />
              <SortableHead label="State" column="state" {...sortProps} />
              <SortableHead
                label="Health"
                column="health"
                className="hidden @lg:table-cell"
                {...sortProps}
              />
              <TableHead className="w-12">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <ListBody
            colSpan={COLUMN_COUNT}
            loading={loading && backends.length === 0}
            // A list that FAILED is not an empty one: "none registered yet"
            // would invite registering a second row for the same store.
            error={
              listError
                ? {
                    what: "Storage backends",
                    reason: listError,
                    onRetry: () => void refetch(),
                  }
                : null
            }
            empty={
              backends.length === 0
                ? {
                    icon: CloudIcon,
                    what: "storage backends",
                    // Three states, not two: the debounce has not fired, the
                    // request is in flight, or the server found nothing.
                    // Collapsing the first two into "no match" tells an
                    // operator their backend is gone while they type.
                    reason: emptyReason({
                      pending: searchPending,
                      filtered: hasFilters,
                    }),
                    purpose:
                      "A storage backend is an S3-compatible store Paladin keeps objects in.",
                    action: {
                      label: "Register the first backend",
                      onClick: () => setCreateOpen(true),
                    },
                  }
                : null
            }
          >
            {sorted.map((b) => (
              <BackendRow
                key={b.backendId}
                backend={b}
                selected={selected.has(b.backendId)}
                busy={busyId === b.backendId}
                onSelect={(on) => toggleSelect(b.backendId, on)}
                onAction={(action) =>
                  action === "disable"
                    ? setConfirmDisable(b)
                    : void runRow(b, action)
                }
              />
            ))}
          </ListBody>
        </Table>
      </ListPage>

      <SelectionBar count={selected.size} onClear={clearSelection}>
        {BULK_GROUPS.map((group, g) => (
          <Fragment key={group[0]}>
            {g > 0 && <Separator orientation="vertical" className="my-1" />}
            {group.map((action) => (
              <Button
                key={action}
                size="sm"
                variant="ghost"
                aria-label={ACTION_TEXT[action].bulkLabel}
                disabled={bulkBusy}
                onClick={() => startBulk(action)}
              >
                {ACTION_TEXT[action].button}
              </Button>
            ))}
          </Fragment>
        ))}
      </SelectionBar>

      <ConfirmModal
        isOpen={confirmDisable !== null}
        onClose={() => setConfirmDisable(null)}
        onConfirm={confirmDisableNow}
        type="warning"
        title={
          confirmDisable === "selection"
            ? `Disable ${selected.size} selected backends?`
            : `Disable ${confirmDisable?.backendId ?? ""}?`
        }
        message={DISABLE_CONSEQUENCE}
        confirmText="Disable"
      />

      <BackendRegisterDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        createBackend={createBackend}
      />
    </TableScope>
  );
}

function BackendRow({
  backend: b,
  selected,
  busy,
  onSelect,
  onAction,
}: {
  backend: StorageBackend;
  selected: boolean;
  busy: boolean;
  onSelect: (on: boolean) => void;
  onAction: (action: Action) => void;
}) {
  const detailHref = `/storage-backends/${encodeURIComponent(b.backendId)}`;
  const provider = providerLabel(b);
  const kind = STORAGE_KIND_LABELS[b.kind] || "—";
  const state = lifecycleState(b);
  const health = healthView(b);
  const compat = compatibilityBadge(b);
  const compatWhy = featureWarnings(b)
    .map((w) => w.text)
    .join("\n");

  return (
    <TableRow className="group" data-state={selected ? "selected" : undefined}>
      <TableCell className="w-10">
        <Checkbox
          aria-label={`Select ${b.backendId}`}
          checked={selected}
          onCheckedChange={(v) => onSelect(v === true)}
        />
      </TableCell>

      <TableCell>
        <Link href={detailHref} className="flex items-center gap-3 min-w-0">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-chart-2/15 text-chart-2 ring-1 ring-chart-2/30">
            <ServerStackIcon className="size-4" />
          </div>
          <div className="min-w-0">
            <div className="font-medium break-all group-hover:underline">
              {b.backendId}
            </div>
            <div className="truncate text-xs text-muted-foreground">
              {b.displayName || <span className="italic">unnamed</span>}
            </div>
          </div>
        </Link>
      </TableCell>

      <TableCell className="hidden @2xl:table-cell">
        <div className="text-sm">
          {provider.label === "—" ? (
            <span className="text-muted-foreground">{kind}</span>
          ) : (
            <span>
              {provider.label}
              {/* A bare "?" used to say this, and said it to nobody. */}
              {provider.derived && (
                <span
                  className="text-muted-foreground"
                  title={GUESSED_PROVIDER_HINT}
                >
                  {" "}
                  {GUESSED_PROVIDER_LABEL}
                </span>
              )}
              <span className="text-muted-foreground"> · {kind}</span>
            </span>
          )}
        </div>
        <div
          className="max-w-72 truncate font-mono text-xs text-muted-foreground"
          title={b.endpoint}
        >
          {b.endpoint || "—"}
        </div>
      </TableCell>

      <TableCell className="hidden @4xl:table-cell font-mono text-xs text-muted-foreground">
        {b.region || "—"}
      </TableCell>

      <TableCell>
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant={state.variant}>{state.label}</Badge>
          {b.maintenance && (
            <Badge variant="warning">
              <WrenchScrewdriverIcon />
              Maintenance
            </Badge>
          )}
        </div>
      </TableCell>

      {/* What the last probes found: connectivity (advisory, does not gate
          ops) and the S3 features Paladin uses (ADR-0026). The features
          badge appears only when they need a look: "Compatible" beside the
          kind's "S3-compatible" read as the same fact twice, and widened the
          table to say nothing. The detail page always shows it. */}
      <TableCell className="hidden @lg:table-cell">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span
            className="inline-flex items-center gap-1.5 text-sm"
            title={health.title}
          >
            <span
              aria-hidden
              className={cn("size-2 rounded-full", HEALTH_DOT[health.tone])}
            />
            {health.label}
          </span>
          {compat.variant !== COMPATIBLE_VARIANT && (
            <Badge variant={compat.variant} title={compatWhy || undefined}>
              {compat.label}
            </Badge>
          )}
        </div>
      </TableCell>

      <TableCell className="text-right">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              disabled={busy}
              aria-label={`Actions for ${b.backendId}`}
            >
              {busy ? (
                <ArrowPathIcon className="size-4 animate-spin" />
              ) : (
                <EllipsisHorizontalIcon className="size-4" />
              )}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem asChild>
              <Link href={detailHref}>
                <ArrowTopRightOnSquareIcon className="size-4" />
                Open details
              </Link>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            {b.enabled && (
              <DropdownMenuItem
                onSelect={() => onAction(b.readOnly ? "undrain" : "drain")}
              >
                {b.readOnly ? (
                  <PlayCircleIcon className="size-4" />
                ) : (
                  <PauseCircleIcon className="size-4" />
                )}
                {b.readOnly ? "Undrain" : "Drain"}
              </DropdownMenuItem>
            )}
            <DropdownMenuItem
              onSelect={() =>
                onAction(b.maintenance ? "unmaintenance" : "maintenance")
              }
            >
              <WrenchScrewdriverIcon className="size-4" />
              {b.maintenance ? "Clear maintenance" : "Flag for maintenance"}
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            {b.enabled ? (
              <DropdownMenuItem
                variant="destructive"
                onSelect={() => onAction("disable")}
              >
                <NoSymbolIcon className="size-4" />
                Disable…
              </DropdownMenuItem>
            ) : (
              <DropdownMenuItem onSelect={() => onAction("enable")}>
                <PlayCircleIcon className="size-4" />
                Enable
              </DropdownMenuItem>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </TableCell>
    </TableRow>
  );
}
