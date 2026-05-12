"use client";

// BackgroundOpsDrawer — TopBar widget showing ongoing/recent
// long-running operations. Polls admin/v1.OperationService.List,
// surfaces counts in a small badge ("2 in progress · 1 failed"),
// expands into a Sheet with retry/cancel actions per op.
//
// Polling interval: 5s while drawer is closed, 2s while open. No
// SSE/WebSocket dependency — keeps the auth surface unchanged and
// matches the existing useSidebarCounts pattern.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { ConnectError } from "@connectrpc/connect";
import {
  ArrowPathIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
  XCircleIcon,
} from "@heroicons/react/24/outline";

import { adminOperationClient } from "@/lib/connect/client";
import type { Operation } from "@/gen/paladin/admin/v1/operation_service_pb";

// Operation.result is a oneof — case "error" carries google.rpc.Status,
// whose `message` field is the human-readable failure. Extract it once
// so the rest of the component stays oneof-agnostic.
function opError(o: Operation): string {
  if (o.result.case === "error") return o.result.value.message || "error";
  return "";
}
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
import { RelativeTime } from "@/components/RelativeTime";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

const POLL_CLOSED_MS = 5_000;
const POLL_OPEN_MS = 2_000;

interface OpsSummary {
  inProgress: number;
  failed: number;
  done: number;
  ops: Operation[];
}

export function BackgroundOpsDrawer() {
  const [open, setOpen] = useState(false);
  const [summary, setSummary] = useState<OpsSummary>({
    inProgress: 0,
    failed: 0,
    done: 0,
    ops: [],
  });
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const fetchOps = useCallback(async () => {
    setLoading(true);
    try {
      const res = await adminOperationClient.listOperations({
        page: { pageSize: 50, pageToken: "" },
        filter: "",
      });
      const ops = res.operations;
      const inProgress = ops.filter((o) => !o.done && !opError(o)).length;
      const failed = ops.filter((o) => !!opError(o)).length;
      const done = ops.filter((o) => o.done && !opError(o)).length;
      setSummary({ inProgress, failed, done, ops });
      setError(null);
    } catch (err) {
      // OperationService may not be wired in all deployments; degrade
      // silently. UI shows the icon dimmed when no ops can be loaded.
      setError(err instanceof ConnectError ? err.rawMessage : "Failed to load");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchOps();
    const interval = open ? POLL_OPEN_MS : POLL_CLOSED_MS;
    const id = setInterval(fetchOps, interval);
    return () => clearInterval(id);
  }, [open, fetchOps]);

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
          ) : summary.ops.length === 0 ? (
            <p className="py-12 text-center text-xs text-muted-foreground">
              No background operations.
            </p>
          ) : (
            <ul className="space-y-2">
              {summary.ops.map((op) => (
                <OpRow key={op.name} op={op} />
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

function OpRow({ op }: { op: Operation }) {
  const inProgress = !op.done && !opError(op);
  const failed = !!opError(op);
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
        </div>
      </div>
    </li>
  );
}
