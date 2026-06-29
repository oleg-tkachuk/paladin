"use client";

// ActivityTimeline — per-resource audit feed. Reuses the existing
// AuditService.ListAuditLog with a CEL `filter` that pins
// resource_name. Renders compact rows: relative time + actor + action
// + before/after diff disclosure.
//
// Phase 1 of canonical-resource-names ensures `resource_name` on
// audit_log rows is the A-shape
// `storageBackends/{b}/buckets/{bk}/tenants/{tid}/objectKeys/{ok}`.
// The C-shape `tenants/{tid}/objectKeys/{ok}` still works as a
// substring match thanks to migration 035's backfill rewriting all
// historical rows to canonical.
//
// Caller passes `resourceName` in whatever shape it has handy; this
// component uses `contains(resource_name, ...)` so either form
// matches.

import React, { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ConnectError } from "@connectrpc/connect";
import {
  ChevronDownIcon,
  ChevronRightIcon,
  UserCircleIcon,
} from "@heroicons/react/24/outline";

import { auditClient } from "@/lib/connect/client";
import type { AuditLogEntry } from "@/gen/paladin/admin/v1/types_pb";
import { Card, CardContent } from "@/components/ui/Card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/Skeleton";
import { RelativeTime } from "@/components/RelativeTime";
import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

const PAGE_SIZE = 25;

export interface ActivityTimelineProps {
  /** Resource name (canonical or C-form). Required. */
  resourceName: string;
  /** Cap the visible rows. Defaults to 25. */
  limit?: number;
  className?: string;
}

export function ActivityTimeline({
  resourceName,
  limit = PAGE_SIZE,
  className,
}: ActivityTimelineProps) {
  const query = useQuery({
    queryKey: ["activityTimeline", resourceName, limit],
    queryFn: ({ signal }) => {
      // CEL: substring match on resource_name so both A-shape (canonical)
      // and C-shape (tenant-first) keys hit. Escape embedded quotes to
      // prevent filter injection. TanStack cancels via `signal` on unmount /
      // prop change — replaces the manual `cancelled` guard.
      const safe = resourceName.replace(/"/g, '\\"');
      return auditClient
        .listAuditLog(
          {
            page: { pageSize: limit, pageToken: "" },
            filter: `resource_name.contains("${safe}")`,
          },
          { signal },
        )
        .then((res) => res.entries);
    },
  });
  const entries = query.data ?? [];
  const loading = query.isLoading;
  const error = query.error
    ? query.error instanceof ConnectError
      ? query.error.rawMessage
      : "Failed to load"
    : null;

  if (loading) {
    return (
      <div className={cn("space-y-2", className)}>
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    );
  }
  if (error) {
    return (
      <Card className={className}>
        <CardContent className="py-6 text-center text-xs text-destructive">
          {error}
        </CardContent>
      </Card>
    );
  }
  if (entries.length === 0) {
    return (
      <Card className={className}>
        <CardContent className="py-6 text-center text-xs text-muted-foreground">
          No activity recorded for this resource yet.
        </CardContent>
      </Card>
    );
  }

  return (
    <ol className={cn("space-y-2", className)}>
      {entries.map((e) => (
        <ActivityRow key={e.entryId} entry={e} />
      ))}
    </ol>
  );
}

function ActivityRow({ entry }: { entry: AuditLogEntry }) {
  const [open, setOpen] = useState(false);
  const hasDiff =
    (entry.beforeJson && entry.beforeJson.length > 0) ||
    (entry.afterJson && entry.afterJson.length > 0);
  const action = (entry.action || "").split("/").pop() || entry.action;
  return (
    <li className="rounded-md border border-border bg-background/40">
      <button
        type="button"
        onClick={() => hasDiff && setOpen((v) => !v)}
        className={cn(
          "flex w-full items-start gap-3 px-3 py-2 text-left text-xs",
          hasDiff && "hover:bg-muted/40",
        )}
        disabled={!hasDiff}
      >
        {hasDiff ? (
          open ? (
            <ChevronDownIcon className="mt-0.5 size-3 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRightIcon className="mt-0.5 size-3 shrink-0 text-muted-foreground" />
          )
        ) : (
          <span className="mt-0.5 size-3 shrink-0" />
        )}
        <UserCircleIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1 space-y-0.5">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <Badge variant="outline" className={T.labelTight}>
              {action}
            </Badge>
            {entry.errorMessage && (
              <Badge variant="destructive" className={T.labelTight}>
                error
              </Badge>
            )}
            <span className="font-medium">
              {entry.actorSubject || "unknown"}
            </span>
            <span className="text-muted-foreground">
              <RelativeTime ts={entry.at} />
            </span>
          </div>
          {entry.errorMessage && (
            <p className="font-mono text-[11px] text-destructive">
              {entry.errorMessage}
            </p>
          )}
        </div>
      </button>
      {open && hasDiff && (
        <div className="grid grid-cols-1 gap-3 border-t border-border bg-muted/20 px-3 py-2 md:grid-cols-2">
          <DiffPane label="before" json={bytesToString(entry.beforeJson)} />
          <DiffPane label="after" json={bytesToString(entry.afterJson)} />
        </div>
      )}
    </li>
  );
}

function bytesToString(b: Uint8Array | undefined): string {
  if (!b || b.length === 0) return "";
  try {
    return new TextDecoder().decode(b);
  } catch {
    return "";
  }
}

function DiffPane({ label, json }: { label: string; json: string }) {
  return (
    <div className="min-w-0 space-y-1">
      <p className="text-[10px] uppercase tracking-wider text-muted-foreground">
        {label}
      </p>
      <pre className="max-h-48 overflow-auto rounded border border-border bg-background/60 p-2 font-mono text-[10px] leading-tight">
        {json || <span className="italic text-muted-foreground">empty</span>}
      </pre>
    </div>
  );
}
