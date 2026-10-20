"use client";

// A resource's history as a vertical timeline, after Argo CD's deployment
// history: each change with how it ended, who made it — a person, automation
// or Paladin — what it was, when, how long it ran, and a link to its record.
// Paladin keeps no revisions of a resource, so there is nothing to roll back
// to; the record says what changed.

import Link from "next/link";
import {
  ArrowPathIcon,
  ClockIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";

import { ActorName } from "@/components/features/audit/ActorName";
import { RelativeTime } from "@/components/RelativeTime";
import { Timestamp } from "@/components/Timestamp";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/Card";
import { EmptyState } from "@/components/ui/EmptyState";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatusIcon } from "@/components/ui/StatusIcon";
import { Button } from "@/components/ui/button";
import type { UseResourceHistoryResult } from "@/hooks/useResourceHistory";
import { formatDuration } from "@/lib/format/duration";
import { ACTOR_KIND_LABEL, type HistoryEntry } from "@/lib/history";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { FOCUS_RING } from "@/lib/ui/states";
import { STATUS_TONE } from "@/lib/ui/status";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** What an entry with no actor recorded is attributed to. */
const PALADIN = "Paladin";

function Actor({ actor }: { actor: HistoryEntry["actor"] }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <Badge variant="outline">{ACTOR_KIND_LABEL[actor.kind]}</Badge>
      <span className="truncate text-foreground">
        {actor.subject ? (
          <ActorName subject={actor.subject} tenantId={actor.tenantId} />
        ) : (
          PALADIN
        )}
      </span>
    </span>
  );
}

function Entry({ entry }: { entry: HistoryEntry }) {
  return (
    <li className="group/entry relative flex gap-3 pb-5 last:pb-0">
      {/* The rule joins each entry to the one before it; the last has none
          below it. */}
      <span
        aria-hidden
        className="absolute top-6 bottom-0 left-2 w-px bg-border group-last/entry:hidden"
      />
      <span className="relative mt-0.5 flex size-4 shrink-0 bg-card">
        <StatusIcon tone={entry.tone} label={entry.outcome} />
      </span>
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span className="font-medium">{entry.action}</span>
          <span className={cn("text-xs", STATUS_TONE[entry.tone].ink)}>
            {entry.outcome}
          </span>
        </div>
        <div
          className={cn(T.hint, "flex flex-wrap items-center gap-x-3 gap-y-1")}
        >
          <Actor actor={entry.actor} />
          {entry.at && (
            <span className="inline-flex items-center gap-1">
              <RelativeTime ts={entry.at} />
              <span aria-hidden>·</span>
              <Timestamp ts={timestampFromDate(entry.at)} />
            </span>
          )}
          {entry.durationMs !== null && (
            <span className="inline-flex items-center gap-1">
              <ClockIcon aria-hidden className="size-3" />
              {formatDuration(entry.durationMs)}
            </span>
          )}
        </div>
        {entry.error && (
          <p className="line-clamp-2 text-xs break-words text-destructive">
            {entry.error}
          </p>
        )}
        <Link
          href={entry.href}
          className={cn(
            "inline-block rounded-sm text-xs text-primary hover:underline",
            FOCUS_RING,
          )}
        >
          {entry.source === "audit" ? "Open audit entry" : "Open operation"}
        </Link>
      </div>
    </li>
  );
}

/**
 * The timeline of one resource. `what` says whose it is for the empty state:
 * "this bucket".
 */
export function HistoryTimeline({
  history,
  what,
  description,
}: {
  history: UseResourceHistoryResult;
  what: string;
  /** What the history holds, under the title. */
  description: string;
}) {
  const { entries, loading, error, operationsError, hasMore } = history;
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2">
        <div className="min-w-0 space-y-0.5">
          <CardTitle icon={ClockIcon}>History</CardTitle>
          <p className={T.hint}>{description}</p>
        </div>
        <Button
          variant="outline"
          size="icon"
          aria-label="Refresh"
          onClick={() => void history.refresh()}
          disabled={loading}
        >
          <ArrowPathIcon className="size-4" />
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        {operationsError && (
          <p
            role="status"
            className="flex items-start gap-2 text-xs text-warning"
          >
            <ExclamationTriangleIcon className="mt-0.5 size-3.5 shrink-0" />
            <span>
              Operations could not be read, so batches are missing:{" "}
              {operationsError}
            </span>
          </p>
        )}
        {error ? (
          <ListLoadError
            what="History"
            reason={error}
            onRetry={() => void history.refresh()}
          />
        ) : loading && entries.length === 0 ? (
          <div className="space-y-3">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : entries.length === 0 ? (
          <div className="py-6">
            <EmptyState
              icon={ClockIcon}
              what={`changes to ${what}`}
              reason="empty"
            />
          </div>
        ) : (
          <ol aria-label="History">
            {entries.map((e) => (
              <Entry key={e.key} entry={e} />
            ))}
          </ol>
        )}
        {hasMore && !error && (
          <div className="flex justify-center">
            <Button
              variant="outline"
              size="sm"
              onClick={() => void history.loadMore()}
              disabled={loading}
            >
              Load older
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
