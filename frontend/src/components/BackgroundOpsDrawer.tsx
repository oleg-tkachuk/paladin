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
import { ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
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
import { RelativeTime } from "@/components/RelativeTime";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

const POLL_OPEN_MS = 2_000;

interface OpsSummary {
  inProgress: number;
  failed: number;
  done: number;
  ops: Operation[];
}

export function BackgroundOpsDrawer() {
  const [open, setOpen] = useState(false);
  const shell = useShell();
  const { operations } = shell;

  const summary = useMemo<OpsSummary>(() => {
    const ops = operations.data ?? [];
    return {
      inProgress: ops.filter((o) => !o.done && !opError(o)).length,
      failed: ops.filter((o) => !!opError(o)).length,
      done: ops.filter((o) => o.done && !opError(o)).length,
      ops,
    };
  }, [operations.data]);

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

  const triggerLabel = useMemo(() => {
    if (summary.inProgress > 0) return `${summary.inProgress} running`;
    if (summary.failed > 0) return `${summary.failed} failed`;
    return "Operations";
  }, [summary]);

  const triggerVariant: "ghost" | "outline" =
    summary.failed > 0 ? "outline" : "ghost";

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button
          variant={triggerVariant}
          size="sm"
          className={cn(
            "gap-1.5 text-xs",
            summary.failed > 0 && "border-destructive/40 text-destructive",
            error && "opacity-40",
          )}
          aria-label="Background operations"
          title="Background operations"
        >
          <ArrowPathIcon
            className={cn("size-4", summary.inProgress > 0 && "animate-spin")}
          />
          <span className="hidden sm:inline">{triggerLabel}</span>
          {summary.inProgress + summary.failed > 0 && (
            <Badge
              variant={summary.failed > 0 ? "destructive" : "secondary"}
              className="ml-1 h-4 px-1 text-[10px]"
            >
              {summary.inProgress + summary.failed}
            </Badge>
          )}
        </Button>
      </SheetTrigger>
      <SheetContent side="right" className="w-full max-w-md p-0">
        <SheetHeader className="border-b border-border px-4 py-3">
          <SheetTitle>Background operations</SheetTitle>
        </SheetHeader>
        <div className="overflow-y-auto p-4">
          <div className="mb-3 grid grid-cols-3 gap-2 text-center text-xs">
            <Tile
              label="In progress"
              value={summary.inProgress}
              accent="text-amber-500"
            />
            <Tile
              label="Failed"
              value={summary.failed}
              accent="text-destructive"
            />
            <Tile
              label="Recent done"
              value={summary.done}
              accent="text-emerald-500"
            />
          </div>
          {loading && summary.ops.length === 0 ? (
            <div className="space-y-2">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-14 w-full" />
              ))}
            </div>
          ) : error ? (
            // Not "no operations" — unknown. An empty list here would read as
            // "nothing is running" while a batch is very possibly running.
            <div className="py-12 text-center text-xs">
              <p className="text-destructive">
                Operations are unavailable — this list is unknown, not empty.
              </p>
              <p className="mt-1 font-mono text-[10px] text-muted-foreground">
                {error}
              </p>
            </div>
          ) : summary.ops.length === 0 ? (
            <p className="py-12 text-center text-xs text-muted-foreground">
              No background operations.
            </p>
          ) : (
            <ul className="space-y-2">
              {summary.ops.map((op) => (
                <OpRow key={op.name} op={op} onCancel={cancelOp} />
              ))}
            </ul>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function Tile({
  label,
  value,
  accent,
}: {
  label: string;
  value: number;
  accent: string;
}) {
  return (
    <div className="rounded-md border border-border bg-muted/30 p-2">
      <p className={cn("text-2xl font-semibold tabular-nums", accent)}>
        {value}
      </p>
      <p className="text-[10px] uppercase tracking-wider text-muted-foreground">
        {label}
      </p>
    </div>
  );
}

function OpRow({
  op,
  onCancel,
}: {
  op: Operation;
  onCancel: (name: string) => Promise<void>;
}) {
  const inProgress = !op.done && !opError(op);
  const failed = !!opError(op);
  const progress = opProgress(op);
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
      setCancelErr(
        err instanceof ConnectError ? err.rawMessage : "Cancel failed",
      );
    } finally {
      setCanceling(false);
    }
  }, [onCancel, op.name]);
  const succeeded = op.done && !opError(op);
  const Icon = failed
    ? XCircleIcon
    : succeeded
      ? CheckCircleIcon
      : inProgress
        ? ArrowPathIcon
        : ExclamationTriangleIcon;
  const tint = failed
    ? "text-destructive"
    : succeeded
      ? "text-emerald-500"
      : "text-amber-500";

  return (
    <li className="rounded-md border border-border bg-background/40 p-2.5 text-xs">
      <div className="flex items-start gap-2">
        <Icon
          className={cn(
            "mt-0.5 size-4 shrink-0",
            tint,
            inProgress && "animate-spin",
          )}
        />
        <div className="min-w-0 flex-1 space-y-0.5">
          <p className="font-medium">{op.type || op.name}</p>
          <p className="truncate font-mono text-[10px] text-muted-foreground">
            {op.name}
          </p>
          <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
            <span>started</span>
            <RelativeTime ts={op.createdAt} />
            {op.done && (
              <Badge variant="outline" className={T.labelTight}>
                done
              </Badge>
            )}
          </div>
          {opError(op) && (
            <p className="mt-1 font-mono text-[10px] text-destructive">
              {opError(op)}
            </p>
          )}
          {progress && (
            <p
              className={cn(
                "mt-1 text-[10px] tabular-nums",
                failed ? "text-destructive" : "text-muted-foreground",
              )}
            >
              {failed ? "reached at least " : ""}
              {progress.processed} of {progress.total}
              {failed ? " before the worker stopped" : " processed"}
            </p>
          )}
          {cancelErr && (
            <p className="mt-1 font-mono text-[10px] text-destructive">
              {cancelErr}
            </p>
          )}
        </div>
        {inProgress && (
          <Button
            variant="outline"
            size="sm"
            className="h-6 shrink-0 px-2 text-[10px]"
            disabled={canceling}
            onClick={() => setConfirmCancel(true)}
          >
            {canceling ? "Canceling…" : "Cancel"}
          </Button>
        )}
      </div>
      <ConfirmModal
        isOpen={confirmCancel}
        onClose={() => setConfirmCancel(false)}
        onConfirm={handleCancel}
        title="Cancel this operation?"
        message={`${op.type || op.name} stops where it is. What it has already done is not undone.`}
        confirmText="Cancel operation"
        cancelText="Keep running"
        loading={canceling}
      />
    </li>
  );
}
