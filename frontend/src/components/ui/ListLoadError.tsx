"use client";

import { ExclamationTriangleIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";

/**
 * The state a list table shows when its fetch failed.
 *
 * Every list page used to fall through to its empty state — "No buckets yet",
 * "No tenants yet" — when the list call errored, because the page read the
 * hook's rows and not its error. That is not a cosmetic difference: it invites
 * the operator to create something that already exists, and it is exactly what
 * an e2e failure looked like from the outside (a seeded bucket "missing" from
 * a page that had simply failed to load).
 */
export function ListLoadError({
  what,
  reason,
  onRetry,
  className,
}: {
  /** Plural noun for the rows, e.g. "Buckets". */
  what: string;
  reason: string;
  onRetry: () => void;
  className?: string;
}) {
  return (
    <div className={className ?? "flex flex-col items-center gap-3"}>
      <ExclamationTriangleIcon className="size-10 text-destructive/60" />
      <p className="text-sm text-destructive">
        {what} could not be loaded — this list is unknown, not empty.
      </p>
      <p className="max-w-md break-words font-mono text-[11px] text-muted-foreground">
        {reason}
      </p>
      <Button size="sm" variant="outline" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}
