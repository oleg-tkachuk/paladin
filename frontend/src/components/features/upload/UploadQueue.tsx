"use client";

import {
  CheckCircleIcon,
  DocumentIcon,
  ExclamationCircleIcon,
  TrashIcon,
} from "@heroicons/react/24/outline";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/Card";
import { Separator } from "@/components/ui/separator";
import type { UploadTask } from "@/hooks/useUpload";
import { cn } from "@/lib/utils";
import { Bytes } from "@/components/ui/Bytes";

/** How many uploads in a queue are in each phase. */
export function queueCounts(queue: readonly UploadTask[]) {
  return {
    active: queue.filter(
      (t) => t.status === "uploading" || t.status === "pending",
    ).length,
    done: queue.filter((t) => t.status === "completed").length,
    failed: queue.filter((t) => t.status === "error").length,
  };
}

/**
 * The uploads a page started, each with its progress, and a count of where
 * they stand. Renders nothing for an empty queue.
 */
export function UploadQueue({
  queue,
  onClear,
}: {
  queue: readonly UploadTask[];
  onClear: () => void;
}) {
  if (queue.length === 0) return null;
  const counts = queueCounts(queue);
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3 text-xs">
          <h3 className="text-sm font-medium text-muted-foreground">
            Upload queue
          </h3>
          <Badge variant="secondary" className="font-mono">
            {queue.length}
          </Badge>
          {counts.active > 0 && (
            <span className="font-mono text-primary">
              {counts.active} active
            </span>
          )}
          {counts.done > 0 && (
            <span className="font-mono text-chart-2">{counts.done} done</span>
          )}
          {counts.failed > 0 && (
            <span className="font-mono text-destructive">
              {counts.failed} failed
            </span>
          )}
        </div>
        <Button size="sm" variant="ghost" onClick={onClear}>
          <TrashIcon className="size-3.5" />
          Clear
        </Button>
      </div>
      <Separator />
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {queue.map((item) => (
          // Stable id, not name+index: new uploads are PREPENDED,
          // so an index-based key shifted every existing row and
          // remounted them (visible progress-bar flicker).
          <QueueRow key={item.id} item={item} />
        ))}
      </div>
    </div>
  );
}

// shadcn doesn't ship Progress by default in our install — fall back inline
// to a Tailwind div so we don't depend on an extra `add` step.
function ProgressBar({ value }: { value: number }) {
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-secondary">
      <div
        className="h-full bg-primary transition-[width] duration-500"
        style={{ width: `${Math.min(100, Math.max(0, value))}%` }}
      />
    </div>
  );
}

function QueueRow({ item }: { item: UploadTask }) {
  const meta = {
    pending: {
      icon: <DocumentIcon className="size-5" />,
      label: "Queued",
      color: "text-muted-foreground",
    },
    uploading: {
      icon: (
        <div className="relative size-5">
          <div className="absolute inset-0 rounded-full border-2 border-primary/20" />
          <div className="absolute inset-0 animate-spin rounded-full border-2 border-primary border-t-transparent" />
        </div>
      ),
      label: `${item.progress}%`,
      color: "text-primary",
    },
    completed: {
      icon: <CheckCircleIcon className="size-5" />,
      label: "Complete",
      color: "text-chart-2",
    },
    error: {
      icon: <ExclamationCircleIcon className="size-5" />,
      label: "Failed",
      color: "text-destructive",
    },
  } as const;
  const m = meta[item.status];

  return (
    <Card className="gap-2 py-3">
      <CardContent className="flex items-center gap-3 px-4">
        <div
          className={cn(
            "flex size-9 shrink-0 items-center justify-center rounded-md bg-secondary",
            m.color,
          )}
        >
          {m.icon}
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">{item.file.name}</div>
          <div className="mt-0.5 flex items-center gap-3 text-xs">
            <span className="font-mono text-muted-foreground tabular-nums">
              <Bytes value={item.file.size} />
            </span>
            <span className={cn("font-medium", m.color)}>{m.label}</span>
            {item.error && (
              <span className="truncate text-destructive" title={item.error}>
                {item.error}
              </span>
            )}
          </div>
        </div>
      </CardContent>
      <div className="px-4">
        <ProgressBar value={item.progress} />
      </div>
    </Card>
  );
}
