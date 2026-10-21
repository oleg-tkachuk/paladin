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
import Link from "next/link";
import {
  ArrowPathIcon,
  ArrowRightIcon,
  XMarkIcon,
} from "@heroicons/react/24/outline";

import { adminOperationClient } from "@/lib/connect/client";
import { useVisiblePolling } from "@/hooks/useVisiblePolling";
import { useShell } from "@/context/ShellContext";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";

import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/Skeleton";
import { Badge } from "@/components/ui/badge";
import { openableRow } from "@/components/ui/RecordDialog";
import { RelativeTime } from "@/components/RelativeTime";
import { cn } from "@/lib/utils";
import { SECTION } from "@/lib/navigation/sections";
import { T } from "@/lib/ui/typography";
import { errorMessage } from "@/hooks/errorContract";
import { formatDuration } from "@/lib/format/duration";
import {
  OP_STATUS_BADGE,
  isActive,
  objectCountLabel,
  operationElapsedMs,
  operationError,
  operationLabel,
  operationProgress,
  operationStatus,
  outcomeLine,
  OP_VIEWS,
  OP_VIEW_KEEPS,
  type OpView,
} from "@/lib/operations";
import {
  CancelOperationButton,
  CollectionLink,
  OperationProgressBar,
  OperationRecord,
  STATUS_ICON,
  STATUS_TINT,
} from "@/components/features/operations/parts";

const POLL_OPEN_MS = 2_000;

export function BackgroundOpsDrawer() {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState<OpView>("all");
  const [opened, setOpened] = useState<Operation | null>(null);
  const shell = useShell();
  const { operations } = shell;

  const ops = useMemo(() => operations.data ?? [], [operations.data]);
  const counts = useMemo(() => {
    const c: Record<OpView, number> = {
      all: 0,
      active: 0,
      failed: 0,
      done: 0,
    };
    for (const o of ops) {
      const s = operationStatus(o);
      for (const f of OP_VIEWS) if (OP_VIEW_KEEPS[f.value](s)) c[f.value]++;
    }
    return c;
  }, [ops]);
  // Work still going leads; the rest keep the server's order, newest first.
  const shown = useMemo(() => {
    const kept = ops.filter((o) => OP_VIEW_KEEPS[filter](operationStatus(o)));
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

  // Clearing takes finished operations off this list for the signed-in user
  // only, on the server: no names clears every finished one.
  const [clearErr, setClearErr] = useState<string | null>(null);
  const dismiss = useCallback(
    async (names: string[]) => {
      setClearErr(null);
      try {
        await adminOperationClient.dismissOperations({ names });
        await shell.refresh();
      } catch (err) {
        setClearErr(errorMessage(err, "Clearing failed"));
      }
    },
    [shell],
  );
  const finished = counts.all - counts.active;

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
              {/* A line of its own: run on after the summary it wrapped in
                  the narrow header and read as more of the same grey text. */}
              <Link
                href={SECTION.operations}
                onClick={() => setOpen(false)}
                className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
              >
                Open the operations page
                <ArrowRightIcon className="size-3" aria-hidden />
              </Link>
              {clearErr ? (
                <p className="text-xs text-destructive">{clearErr}</p>
              ) : null}
            </div>
            <div className="flex items-center gap-1">
              <Button
                variant="ghost"
                size="sm"
                className="h-8 px-2 text-xs"
                disabled={finished === 0}
                onClick={() => void dismiss([])}
              >
                Clear finished
                {finished > 0 ? (
                  <>
                    {" "}
                    <span className="tabular-nums text-muted-foreground">
                      {finished}
                    </span>
                  </>
                ) : null}
              </Button>
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
          </div>
          <div role="group" aria-label="Show" className="flex flex-wrap gap-1">
            {OP_VIEWS.map((f) => (
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
                  onDismiss={() => dismiss([op.name])}
                  onOpen={() => setOpened(op)}
                  onNavigate={() => setOpen(false)}
                />
              ))}
            </ul>
          )}
        </div>
      </SheetContent>
      <OperationRecord op={opened} onClose={() => setOpened(null)} />
    </Sheet>
  );
}

function OpRow({
  op,
  onCancel,
  onDismiss,
  onOpen,
  onNavigate,
}: {
  op: Operation;
  onCancel: (name: string) => Promise<void>;
  onDismiss: () => Promise<void>;
  onOpen: () => void;
  onNavigate: () => void;
}) {
  const status = operationStatus(op);
  const failed = status === "failed";
  const progress = operationProgress(op);
  const badge = OP_STATUS_BADGE[status];
  const Icon = STATUS_ICON[status];
  const elapsed = operationElapsedMs(op, new Date());
  const outcome = outcomeLine(op);
  // The row reports its own cancel failure rather than the drawer's banner.
  const [cancelErr, setCancelErr] = useState<string | null>(null);
  const row = openableRow(onOpen);

  return (
    <li
      {...row}
      aria-label={`${operationLabel(op)}, ${badge.label}`}
      className={cn(
        row.className,
        "rounded-lg border border-border bg-background/40 p-3 text-sm",
        failed && "border-destructive/30",
        status === "partial" && "border-warning/30",
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
              <span>{objectCountLabel(op.target.objectCount)}</span>
            </p>
          ) : null}
          {/* Only work still going has progress to watch; a finished batch
              says how it ended below instead. */}
          {progress && isActive(status) ? (
            <OperationProgressBar progress={progress} />
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
              {operationError(op)}
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
          <CancelOperationButton
            op={op}
            onCancel={onCancel}
            onError={setCancelErr}
          />
        ) : (
          <Button
            variant="ghost"
            size="icon"
            className="size-7 shrink-0 text-muted-foreground"
            aria-label={`Clear ${operationLabel(op)}`}
            title="Clear from this list"
            onClick={() => void onDismiss()}
          >
            <XMarkIcon className="size-4" />
          </Button>
        )}
      </div>
    </li>
  );
}
