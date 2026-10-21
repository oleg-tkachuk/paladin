"use client";

import { EmptyTableRow } from "@/components/ui/EmptyState";
import { emptyReason } from "@/lib/emptyState";
import { useCallback, useMemo, useState } from "react";

import { useTableFilter, useTableQuery } from "@/hooks/useTableUrl";
import { PAGE_TABLE } from "@/lib/table/urlState";
import {
  ArrowPathIcon,
  ExclamationTriangleIcon,
  QueueListIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";

import { LiveToggle } from "@/components/ui/LiveToggle";
import { liveState } from "@/lib/live";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { ListPage } from "@/components/layout/templates/ListPage";
import { Skeleton } from "@/components/ui/Skeleton";
import { Switch } from "@/components/ui/switch";
import { TableToolbar } from "@/components/ui/TableToolbar";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  SHOWN_IN_CARD,
} from "@/components/ui/table";
import { openableRow } from "@/components/ui/RecordDialog";
import { useDetailPanel, type DetailRowProps } from "@/hooks/useDetailPanel";
import { Timestamp } from "@/components/Timestamp";
import { ActorName } from "@/components/features/audit/ActorName";
import {
  CancelOperationButton,
  CollectionLink,
  OperationProgressBar,
  OperationPanel,
  STATUS_ICON,
  STATUS_TINT,
} from "@/components/features/operations/parts";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";
import {
  OPERATIONS_POLL_MS,
  useOperations,
  type OpScope,
} from "@/hooks/useOperations";
import { errorMessage } from "@/hooks/errorContract";
import { adminOperationClient } from "@/lib/connect/client";
import { formatDuration } from "@/lib/format/duration";
import {
  OP_STATUS_BADGE,
  DEFAULT_OP_VIEW,
  OP_VIEWS,
  isActive,
  objectCountLabel,
  operationElapsedMs,
  operationError,
  operationLabel,
  operationMatches,
  operationProgress,
  operationStatus,
  outcomeLine,
  operationId,
} from "@/lib/operations";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

// /operations — the long-running work behind the console's batch actions, in
// more detail than the drawer has room for: who started it, how far it got,
// how long it took, and the tenant's as well as the user's own. Live, it asks
// again every few seconds while the tab is visible.

const SCOPES: { value: OpScope; label: string }[] = [
  { value: "mine", label: "Mine" },
  { value: "tenant", label: "Tenant" },
];
const COLUMNS = 7;
// Optional columns appear by the width of the table's card, most useful
// first; a phone's card shows the target and the start again.
const COL_TARGET = cn("hidden @2xl:table-cell", SHOWN_IN_CARD);
const COL_STARTED = cn("hidden @3xl:table-cell", SHOWN_IN_CARD);
const COL_DURATION = "hidden @4xl:table-cell";
const COL_STARTED_BY = "hidden @5xl:table-cell";
const SKELETON_ROWS = [0, 1, 2, 3];

export default function OperationsPage() {
  const [view, setView] = useTableFilter(OP_VIEWS, DEFAULT_OP_VIEW, PAGE_TABLE);
  const [scope, setScope] = useState<OpScope>("mine");
  const [includeDismissed, setIncludeDismissed] = useState(false);
  const [live, setLive] = useState(true);
  const [search, setSearch] = useTableQuery(PAGE_TABLE);
  // The open record is in the URL, so an open record's address links to it.
  // The row clicked stays at hand for one the list no longer holds.
  const linked = useDetailPanel();
  const [clicked, setClicked] = useState<Operation | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);

  const { operations, loading, error, hasMore, refresh, loadMore } =
    useOperations({ view, scope, includeDismissed, live });
  const shown = useMemo(
    () => operations.filter((op) => operationMatches(op, search)),
    [operations, search],
  );
  // Clearing marks the user's own list only (the server matches the
  // initiator), so it is offered on that list alone: on the tenant's, a clear
  // of someone else's operation would change nothing and say nothing.
  const mine = scope === "mine";
  const finished = operations.filter(
    (op) => !isActive(operationStatus(op)) && !op.dismissedAt,
  ).length;

  const cancel = useCallback(
    async (name: string) => {
      await adminOperationClient.cancelOperation({ name });
      await refresh();
    },
    [refresh],
  );
  const dismiss = useCallback(
    async (names: string[]) => {
      setActionErr(null);
      try {
        await adminOperationClient.dismissOperations({ names });
        await refresh();
      } catch (err) {
        setActionErr(errorMessage(err, "Clearing failed"));
      }
    },
    [refresh],
  );

  return (
    <>
      <ListPage
        header={
          <PageHeader
            title="Operations"
            description="Batch work started from the console — deletes, copies, tag updates, restores — as it runs and how it ended."
            showDefaultActions={false}
            actions={
              <>
                <LiveToggle
                  state={liveState({ live })}
                  onToggle={() => setLive((v) => !v)}
                  intervalMs={OPERATIONS_POLL_MS}
                />
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void refresh()}
                  disabled={loading}
                >
                  <ArrowPathIcon
                    className={cn("size-4", loading && "animate-spin")}
                  />
                  Refresh
                </Button>
                {mine ? (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={finished === 0}
                    onClick={() => void dismiss([])}
                  >
                    <XMarkIcon className="size-4" />
                    Clear finished
                  </Button>
                ) : null}
              </>
            }
          />
        }
        toolbar={
          <>
            <TableToolbar
              query={search}
              onQueryChange={setSearch}
              searchLabel="Search the operations"
              placeholder="Search by operation, collection, error…"
              filter={{
                label: "Show",
                options: OP_VIEWS,
                value: view,
                onChange: setView,
              }}
              count={{ shown: shown.length, total: operations.length }}
            >
              <div role="group" aria-label="Whose" className="flex gap-1">
                {SCOPES.map((s) => (
                  <Button
                    key={s.value}
                    type="button"
                    size="sm"
                    variant={s.value === scope ? "secondary" : "ghost"}
                    aria-pressed={s.value === scope}
                    onClick={() => setScope(s.value)}
                  >
                    {s.label}
                  </Button>
                ))}
              </div>
              {mine ? (
                <label className={cn(T.hint, "flex items-center gap-2")}>
                  <Switch
                    checked={includeDismissed}
                    onCheckedChange={setIncludeDismissed}
                    aria-label="Show cleared"
                  />
                  Show cleared
                </label>
              ) : null}
            </TableToolbar>

            {error || actionErr ? (
              <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                <ExclamationTriangleIcon className="mt-0.5 size-4 shrink-0" />
                <span>{error ?? actionErr}</span>
              </div>
            ) : null}
          </>
        }
        footer={
          hasMore ? (
            <div className="flex justify-center">
              <Button
                variant="outline"
                size="sm"
                onClick={() => void loadMore()}
                disabled={loading}
              >
                Load more
              </Button>
            </div>
          ) : null
        }
      >
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Operation</TableHead>
              <TableHead className={COL_TARGET}>Target</TableHead>
              <TableHead className="min-w-40">Status</TableHead>
              <TableHead className={COL_STARTED_BY}>Started by</TableHead>
              <TableHead className={COL_STARTED}>Started</TableHead>
              <TableHead className={COL_DURATION}>Duration</TableHead>
              <TableHead className="w-16">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && operations.length === 0 ? (
              SKELETON_ROWS.map((i) => (
                <TableRow key={`s-${i}`}>
                  <TableCell colSpan={COLUMNS} className="py-3">
                    <Skeleton className="h-7 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : error ? (
              // Not "no operations" — unknown. An empty table would read as
              // "nothing is running" while a batch very possibly is.
              <TableRow>
                <TableCell
                  colSpan={COLUMNS}
                  className="h-40 text-center text-sm text-muted-foreground"
                >
                  The operations could not be read — this list is unknown, not
                  empty.
                </TableCell>
              </TableRow>
            ) : shown.length === 0 ? (
              <EmptyTableRow
                colSpan={COLUMNS}
                icon={QueueListIcon}
                what="operations"
                reason={emptyReason({
                  filtered: operations.length > 0 || view !== DEFAULT_OP_VIEW,
                })}
              />
            ) : (
              shown.map((op) => (
                <OperationRow
                  key={op.name}
                  op={op}
                  canDismiss={mine}
                  onCancel={cancel}
                  onDismiss={() => dismiss([op.name])}
                  onOpen={() => {
                    setClicked(op);
                    linked.open(op.name);
                  }}
                  linkedProps={linked.rowProps(op.name)}
                />
              ))
            )}
          </TableBody>
        </Table>
      </ListPage>

      <OperationPanel
        op={
          // The record follows the live list, so an open one keeps moving.
          linked.openId
            ? (operations.find((o) => o.name === linked.openId) ??
              (clicked?.name === linked.openId ? clicked : null))
            : null
        }
        onClose={linked.close}
        onRetried={() => void refresh()}
      />
    </>
  );
}

function OperationRow({
  op,
  canDismiss,
  onCancel,
  onDismiss,
  onOpen,
  linkedProps,
}: {
  op: Operation;
  canDismiss: boolean;
  onCancel: (name: string) => Promise<void>;
  onDismiss: () => Promise<void>;
  onOpen: () => void;
  linkedProps: DetailRowProps;
}) {
  const status = operationStatus(op);
  const badge = OP_STATUS_BADGE[status];
  const Icon = STATUS_ICON[status];
  const progress = operationProgress(op);
  const elapsed = operationElapsedMs(op, new Date());
  const outcome = outcomeLine(op);
  const failure = operationError(op);
  const active = isActive(status);
  const [cancelErr, setCancelErr] = useState<string | null>(null);
  const row = openableRow(onOpen);

  return (
    <TableRow
      {...row}
      {...linkedProps}
      aria-label={`${operationLabel(op)}, ${badge.label}`}
      className={cn(
        row.className,
        status === "failed" && "bg-destructive/5",
        status === "partial" && "bg-warning/5",
      )}
    >
      {/* What it is and which one: the state is the icon's shape and colour,
          and the status column's words. */}
      <TableCell>
        <div className="flex items-start gap-2">
          <Icon
            aria-hidden
            className={cn(
              "mt-0.5 size-4 shrink-0",
              STATUS_TINT[status],
              status === "running" && "animate-spin",
            )}
          />
          <div className="min-w-0">
            <p className="font-medium">{operationLabel(op)}</p>
            <p className={cn(T.hint, "flex items-center gap-1.5")}>
              <span
                className="block max-w-24 truncate font-mono @xl:max-w-40"
                title={op.name}
              >
                {operationId(op)}
              </span>
              {op.dismissedAt ? (
                <>
                  <span aria-hidden>·</span>
                  <span>cleared</span>
                </>
              ) : null}
            </p>
          </div>
        </div>
      </TableCell>
      <TableCell className={cn(COL_TARGET, "whitespace-normal")}>
        {op.target ? (
          <div className="space-y-0.5">
            <p className="flex flex-wrap items-center gap-x-1.5">
              <CollectionLink name={op.target.collection} />
              {op.target.destination ? (
                <>
                  <span aria-hidden>→</span>
                  <CollectionLink name={op.target.destination} />
                </>
              ) : null}
            </p>
            <p className={T.hint}>{objectCountLabel(op.target.objectCount)}</p>
          </div>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </TableCell>
      <TableCell className="whitespace-normal">
        <div className="space-y-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <Badge variant={badge.variant}>{badge.label}</Badge>
            {outcome ? <span className={T.hint}>{outcome}</span> : null}
          </div>
          {progress && active ? (
            <OperationProgressBar progress={progress} />
          ) : null}
          {failure ? (
            <p className="line-clamp-2 text-xs text-destructive">
              {failure}
              {progress
                ? ` — reached at least ${progress.processed} of ${progress.total}`
                : ""}
            </p>
          ) : null}
          {cancelErr ? (
            <p className="text-xs text-destructive">{cancelErr}</p>
          ) : null}
        </div>
      </TableCell>
      <TableCell className={cn(COL_STARTED_BY, "whitespace-normal break-all")}>
        {op.initiatorSubject ? (
          <ActorName
            subject={op.initiatorSubject}
            tenantId={op.initiatorTenantId}
          />
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </TableCell>
      <TableCell
        className={cn(
          T.codeSmall,
          COL_STARTED,
          "whitespace-nowrap text-muted-foreground",
        )}
      >
        {op.createdAt ? <Timestamp ts={op.createdAt} /> : "—"}
      </TableCell>
      <TableCell className={cn(COL_DURATION, "tabular-nums")}>
        {elapsed !== null ? formatDuration(elapsed) : "—"}
      </TableCell>
      <TableCell className="text-right">
        {active ? (
          <CancelOperationButton
            op={op}
            onCancel={onCancel}
            onError={setCancelErr}
          />
        ) : canDismiss && !op.dismissedAt ? (
          <Button
            variant="ghost"
            size="icon"
            className="size-7 text-muted-foreground"
            aria-label={`Clear ${operationLabel(op)}`}
            title="Clear from your list"
            onClick={() => void onDismiss()}
          >
            <XMarkIcon className="size-4" />
          </Button>
        ) : null}
      </TableCell>
    </TableRow>
  );
}
