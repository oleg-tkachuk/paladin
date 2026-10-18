"use client";

// BackgroundOpsDrawer — TopBar widget showing ongoing/recent
// long-running operations. Surfaces counts in a small badge ("2 in progress ·
// 1 failed") and expands into a Sheet with cancel actions per op.
//
// The list arrives with the rest of the shell (ShellContext / /api/shell)
// rather than from a poller of its own: this widget is on every page, and its
// own listOperations call was one of the ~6 chrome RPCs each page used to pay
// for. While the sheet is open it asks the shell to refresh faster than the
// shell's own cadence — still one request, just more often.

import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ClockIcon,
  NoSymbolIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import { adminOperationClient } from "@/lib/connect/client";
import {
  progressFromFailure,
  progressFromMetadata,
  type OpProgress,
} from "@/lib/operationProgress";
import { useVisiblePolling } from "@/hooks/useVisiblePolling";
import { useShell } from "@/context/ShellContext";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";

// Operation.result is a oneof — case "error" carries google.rpc.Status,
// whose `message` field is the human-readable failure. Extract it once
// so the rest of the component stays oneof-agnostic.
function opError(o: Operation): string {
  if (o.result.case === "error") return o.result.value.message || "error";
  return "";
}

// How far the work got — live for a running operation, last-known for one
// whose worker died. A failure with no snapshot returns null and the row says
// nothing rather than guessing zero.
function opProgress(o: Operation): OpProgress | null {
  if (o.result.case === "error") {
    return progressFromFailure(o.result.value.details);
  }
  return progressFromMetadata(o.metadata);
}
import Link from "next/link";

import { Button } from "@/components/ui/button";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/Skeleton";
import { Badge } from "@/components/ui/badge";
import {
  RecordBlock,
  RecordDialog,
  RecordField,
  openableRow,
} from "@/components/ui/RecordDialog";
import { RelativeTime } from "@/components/RelativeTime";
import { Timestamp } from "@/components/Timestamp";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";
import { errorMessage } from "@/hooks/errorContract";
import { collectionHref } from "@/lib/navigation/hrefs";
import { formatDuration } from "@/lib/format/duration";
import {
  OP_STATUS_BADGE,
  isActive,
  operationElapsedMs,
  operationLabel,
  operationOutcome,
  operationStatus,
  parseCollectionName,
  type OpStatus,
} from "@/lib/operations";

const POLL_OPEN_MS = 2_000;

// Which operations the list shows.
type OpFilter = "all" | "active" | "failed" | "done";
const OP_FILTERS: { value: OpFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "active", label: "Active" },
  { value: "failed", label: "Failed" },
  { value: "done", label: "Done" },
];
const KEEPS: Record<OpFilter, (s: OpStatus) => boolean> = {
  all: () => true,
  active: isActive,
  failed: (s) => s === "failed",
  done: (s) => s === "succeeded" || s === "cancelled",
};

const STATUS_ICON = {
  pending: ClockIcon,
  running: ArrowPathIcon,
  succeeded: CheckCircleIcon,
  failed: XCircleIcon,
  cancelled: NoSymbolIcon,
} as const;
const STATUS_TINT: Record<OpStatus, string> = {
  pending: "text-muted-foreground",
  running: "text-warning",
  succeeded: "text-success",
  failed: "text-destructive",
  cancelled: "text-muted-foreground",
};

export function BackgroundOpsDrawer() {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState<OpFilter>("all");
  const [opened, setOpened] = useState<Operation | null>(null);
  const shell = useShell();
  const { operations } = shell;

  const ops = useMemo(() => operations.data ?? [], [operations.data]);
  const counts = useMemo(() => {
    const c: Record<OpFilter, number> = {
      all: 0,
      active: 0,
      failed: 0,
      done: 0,
    };
    for (const o of ops) {
      const s = operationStatus(o);
      for (const f of OP_FILTERS) if (KEEPS[f.value](s)) c[f.value]++;
    }
    return c;
  }, [ops]);
  // Work still going leads; the rest keep the server's order, newest first.
  const shown = useMemo(() => {
    const kept = ops.filter((o) => KEEPS[filter](operationStatus(o)));
    return [
      ...kept.filter((o) => isActive(operationStatus(o))),
      ...kept.filter((o) => !isActive(operationStatus(o))),
    ];
  }, [ops, filter]);

  // The section reports its own state: "unavailable" dims the trigger and
  // says why, rather than showing an empty list as if nothing were running.
  const error =
    operations.status === "unavailable"
      ? (operations.reason ?? "Failed to load")
      : null;
  const loading = operations.status === "loading";

  // Cancel an in-flight operation, then refresh so the row reflects the new
  // state immediately (the poll would catch it within a tick anyway).
  const cancelOp = useCallback(
    async (name: string) => {
      await adminOperationClient.cancelOperation({ name });
      await shell.refresh();
    },
    [shell],
  );

  // While the sheet is open the operator is watching a batch move, so ask the
  // shell for a fresh copy faster than its own cadence. Closed, the shell's
  // poll is enough and this ticks into a no-op.
  const openRef = useRef(open);
  useEffect(() => {
    openRef.current = open;
  }, [open]);
  const refreshWhileOpen = useCallback(() => {
    if (openRef.current) void shell.refresh();
  }, [shell]);
  useVisiblePolling(refreshWhileOpen, POLL_OPEN_MS);

  const triggerLabel =
    counts.active > 0
      ? `${counts.active} running`
      : counts.failed > 0
        ? `${counts.failed} failed`
        : "Operations";
  const standing =
    counts.active + counts.failed === 0
      ? "nothing running"
      : [
          counts.active > 0 && `${counts.active} active`,
          counts.failed > 0 && `${counts.failed} failed`,
        ]
          .filter(Boolean)
          .join(" · ");
  // The list is the signed-in user's own (the shell asks with mine).
  const summary = `Yours only · ${standing}`;

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button
          variant={counts.failed > 0 ? "outline" : "ghost"}
          size="sm"
          className={cn(
            "gap-1.5 text-xs",
            counts.failed > 0 && "border-destructive/40 text-destructive",
            error && "opacity-40",
          )}
          aria-label="Background operations"
          title="Background operations"
        >
          <ArrowPathIcon
            className={cn("size-4", counts.active > 0 && "animate-spin")}
          />
          <span className="hidden sm:inline">{triggerLabel}</span>
          {counts.active + counts.failed > 0 && (
            <Badge
              variant={counts.failed > 0 ? "destructive" : "secondary"}
              className="ml-1 px-1"
            >
              {counts.active + counts.failed}
            </Badge>
          )}
        </Button>
      </SheetTrigger>
      <SheetContent
        side="right"
        className="flex w-full flex-col gap-0 p-0 sm:max-w-md"
      >
        <SheetHeader className="gap-3 border-b border-border px-4 py-3">
          <div className="flex items-start justify-between gap-2 pr-8">
            <div>
              <SheetTitle>Background operations</SheetTitle>
              <p className={T.hint}>{summary}</p>
            </div>
            <Button
              variant="ghost"
              size="icon"
              className="size-8"
              aria-label="Refresh operations"
              onClick={() => void shell.refresh()}
            >
              <ArrowPathIcon
                className={cn("size-4", loading && "animate-spin")}
              />
            </Button>
          </div>
          <div role="group" aria-label="Show" className="flex flex-wrap gap-1">
            {OP_FILTERS.map((f) => (
              <Button
                key={f.value}
                type="button"
                size="sm"
                variant={f.value === filter ? "secondary" : "ghost"}
                aria-pressed={f.value === filter}
                onClick={() => setFilter(f.value)}
                className="h-7 gap-1.5 px-2 text-xs"
              >
                {f.label}{" "}
                <span className="tabular-nums text-muted-foreground">
                  {counts[f.value]}
                </span>
              </Button>
            ))}
          </div>
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-y-auto p-3">
          {loading && ops.length === 0 ? (
            <div className="space-y-2">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-16 w-full" />
              ))}
            </div>
          ) : error ? (
            // Not "no operations" — unknown. An empty list here would read as
            // "nothing is running" while a batch is very possibly running.
            <div className="py-12 text-center text-xs">
              <p className="text-destructive">
                Operations are unavailable — this list is unknown, not empty.
              </p>
              <p className="mt-1 font-mono text-sm text-muted-foreground">
                {error}
              </p>
            </div>
          ) : shown.length === 0 ? (
            <p className="py-12 text-center text-xs text-muted-foreground">
              {ops.length === 0
                ? "No background operations."
                : "No operation in this state."}
            </p>
          ) : (
            <ul className="space-y-2">
              {shown.map((op) => (
                <OpRow
                  key={op.name}
                  op={op}
                  onCancel={cancelOp}
                  onOpen={() => setOpened(op)}
                  onNavigate={() => setOpen(false)}
                />
              ))}
            </ul>
          )}
        </div>
      </SheetContent>
      <OpRecord op={opened} onClose={() => setOpened(null)} />
    </Sheet>
  );
}

/** The collection an operation acts on, as a link that closes the drawer. */
function CollectionLink({
  name,
  onNavigate,
}: {
  name: string;
  onNavigate?: () => void;
}) {
  const c = parseCollectionName(name);
  if (!c) return <span className={T.code}>{name}</span>;
  return (
    <Link
      href={collectionHref(c.tenant, c.collection)}
      onClick={onNavigate}
      className={cn(T.code, "break-all hover:text-primary hover:underline")}
      title={name}
    >
      {c.collection}
    </Link>
  );
}

const objects = (n: number) => `${n} object${n === 1 ? "" : "s"}`;

/** One sentence of how a finished batch went. */
function outcomeLine(op: Operation): string | null {
  const o = operationOutcome(op);
  if (!o) return null;
  const parts = [`${o.succeeded} of ${o.total} done`];
  if (o.alreadyApplied > 0) parts.push(`${o.alreadyApplied} already so`);
  if (o.failed > 0) parts.push(`${o.failed} failed`);
  return parts.join(" · ");
}

function OpRow({
  op,
  onCancel,
  onOpen,
  onNavigate,
}: {
  op: Operation;
  onCancel: (name: string) => Promise<void>;
  onOpen: () => void;
  onNavigate: () => void;
}) {
  const status = operationStatus(op);
  const failed = status === "failed";
  const progress = opProgress(op);
  const badge = OP_STATUS_BADGE[status];
  const Icon = STATUS_ICON[status];
  const elapsed = operationElapsedMs(op, new Date());
  const outcome = outcomeLine(op);
  // Local per-row state so the button disables + reports its own failure
  // without coupling to the drawer's shared error banner.
  const [canceling, setCanceling] = useState(false);
  const [cancelErr, setCancelErr] = useState<string | null>(null);
  // A batch stopped part-way is not undone by starting it again: the objects
  // it already moved or deleted stay moved or deleted. One click on a small
  // button in a drawer used to do that.
  const [confirmCancel, setConfirmCancel] = useState(false);
  const handleCancel = useCallback(async () => {
    setCanceling(true);
    setCancelErr(null);
    try {
      await onCancel(op.name);
    } catch (err) {
      setCancelErr(errorMessage(err, "Cancel failed"));
    } finally {
      setCanceling(false);
    }
  }, [onCancel, op.name]);
  const row = openableRow(onOpen);

  return (
    <li
      {...row}
      aria-label={`${operationLabel(op)}, ${badge.label}`}
      className={cn(
        row.className,
        "rounded-lg border border-border bg-background/40 p-3 text-sm",
        failed && "border-destructive/30",
      )}
    >
      <div className="flex items-start gap-2.5">
        <Icon
          className={cn(
            "mt-0.5 size-4 shrink-0",
            STATUS_TINT[status],
            status === "running" && "animate-spin",
          )}
        />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex items-center justify-between gap-2">
            <p className="truncate font-medium">{operationLabel(op)}</p>
            <Badge variant={badge.variant}>{badge.label}</Badge>
          </div>
          {op.target ? (
            <p className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-muted-foreground">
              <CollectionLink
                name={op.target.collection}
                onNavigate={onNavigate}
              />
              {op.target.destination ? (
                <>
                  <span aria-hidden>→</span>
                  <CollectionLink
                    name={op.target.destination}
                    onNavigate={onNavigate}
                  />
                </>
              ) : null}
              <span aria-hidden>·</span>
              <span>{objects(op.target.objectCount)}</span>
            </p>
          ) : null}
          {progress && !failed ? (
            <div className="space-y-0.5">
              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-warning transition-[width]"
                  style={{
                    width: `${Math.min(100, (progress.processed / progress.total) * 100)}%`,
                  }}
                />
              </div>
              <p className={cn(T.hint, "tabular-nums")}>
                {progress.processed} of {progress.total} processed
              </p>
            </div>
          ) : null}
          <p className={cn(T.hint, "flex flex-wrap gap-x-1.5")}>
            <span>
              started <RelativeTime ts={op.createdAt} />
            </span>
            {elapsed !== null ? (
              <span>
                · {isActive(status) ? "for" : "took"} {formatDuration(elapsed)}
              </span>
            ) : null}
            {outcome ? <span>· {outcome}</span> : null}
          </p>
          {failed ? (
            <p className="line-clamp-2 text-xs text-destructive">
              {opError(op)}
              {progress
                ? ` — reached at least ${progress.processed} of ${progress.total} before the worker stopped`
                : ""}
            </p>
          ) : null}
          {cancelErr ? (
            <p className="text-xs text-destructive">{cancelErr}</p>
          ) : null}
        </div>
        {isActive(status) ? (
          <Button
            variant="outline"
            size="sm"
            className="h-7 shrink-0 px-2 text-xs"
            disabled={canceling}
            onClick={() => setConfirmCancel(true)}
          >
            {canceling ? "Canceling…" : "Cancel"}
          </Button>
        ) : null}
      </div>
      <ConfirmModal
        isOpen={confirmCancel}
        onClose={() => setConfirmCancel(false)}
        onConfirm={handleCancel}
        title="Cancel this operation?"
        message={`${operationLabel(op)} stops where it is. What it has already done is not undone.`}
        confirmText="Cancel operation"
        cancelText="Keep running"
        loading={canceling}
      />
    </li>
  );
}

/** An operation's whole record: what it was asked, how it went, why not. */
function OpRecord({
  op,
  onClose,
}: {
  op: Operation | null;
  onClose: () => void;
}) {
  const status = op ? operationStatus(op) : null;
  const outcome = op ? operationOutcome(op) : null;
  const progress = op ? opProgress(op) : null;
  const elapsed = op ? operationElapsedMs(op, new Date()) : null;
  return (
    <RecordDialog
      open={op !== null}
      onOpenChange={(o) => !o && onClose()}
      title={op ? operationLabel(op) : "Operation"}
      subtitle={op?.name}
    >
      {op && status ? (
        <>
          <RecordField label="State">
            <Badge variant={OP_STATUS_BADGE[status].variant}>
              {OP_STATUS_BADGE[status].label}
            </Badge>
          </RecordField>
          <RecordField label="Type" mono>
            {op.type}
          </RecordField>
          {op.target ? (
            <>
              <RecordField label="Collection">
                <CollectionLink name={op.target.collection} />
              </RecordField>
              {op.target.destination ? (
                <RecordField label="Copied into">
                  <CollectionLink name={op.target.destination} />
                </RecordField>
              ) : null}
              <RecordField label="Objects">
                {objects(op.target.objectCount)}
              </RecordField>
            </>
          ) : null}
          <RecordField label="Started">
            {op.createdAt ? <Timestamp ts={op.createdAt} /> : "—"}
          </RecordField>
          {op.doneAt ? (
            <RecordField label="Finished">
              <Timestamp ts={op.doneAt} />
            </RecordField>
          ) : null}
          {elapsed !== null ? (
            <RecordField label={isActive(status) ? "Running for" : "Took"}>
              {formatDuration(elapsed)}
            </RecordField>
          ) : null}
          {progress ? (
            <RecordField label="Progress">
              {progress.processed} of {progress.total}
            </RecordField>
          ) : null}
          {outcome ? (
            <RecordField label="Result">
              {outcome.succeeded} done
              {outcome.alreadyApplied > 0
                ? `, ${outcome.alreadyApplied} already so`
                : ""}
              {outcome.failed > 0 ? `, ${outcome.failed} failed` : ""} of{" "}
              {outcome.total}
            </RecordField>
          ) : null}
          {opError(op) ? (
            <RecordField label="Error">
              <span className="text-destructive">{opError(op)}</span>
            </RecordField>
          ) : null}
          {outcome && outcome.failures.length > 0 ? (
            <RecordBlock label="Objects that failed">
              {outcome.failures
                .map((f) => `${f.objectId}  ${f.reason}`)
                .join("\n")}
            </RecordBlock>
          ) : null}
        </>
      ) : null}
    </RecordDialog>
  );
}
