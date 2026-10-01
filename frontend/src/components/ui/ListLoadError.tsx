"use client";

import { ExclamationTriangleIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";

/** A read that failed: what the server said, and how to try again. */
export interface FailedRead {
  reason: string;
  retry: () => void;
}

/**
 * The FailedRead for a state-only query hook (errorContract.ts), or null when
 * it has not failed. `retry` re-runs the hook's fetch and drops its result.
 */
export function failedRead(
  error: string | null,
  retry: () => unknown,
): FailedRead | null {
  return error ? { reason: error, retry: () => void retry() } : null;
}

/**
 * The state a list shows when its fetch failed.
 *
 * Every list page used to fall through to its empty state — "No buckets yet",
 * "No tenants yet" — when the list call errored, because the page read the
 * hook's rows and not its error. That is not a cosmetic difference: it invites
 * the operator to create something that already exists, and it is exactly what
 * an e2e failure looked like from the outside (a seeded bucket "missing" from
 * a page that had simply failed to load).
 *
 * Two shapes of one state. `block` stands in for a table. `inline` sits where
 * a select or a short list would be — a dialog field, a form row — which had
 * no failed state at all: a select whose options failed to load rendered as a
 * select with nothing to choose, which reads as "there is nothing".
 */
export function ListLoadError({
  what,
  reason,
  onRetry,
  className,
  variant = "block",
}: {
  /** Plural noun for the rows, e.g. "Buckets". */
  what: string;
  reason: string;
  onRetry: () => void;
  className?: string;
  variant?: "block" | "inline";
}) {
  if (variant === "inline") {
    return (
      <div
        role="alert"
        className={
          className ??
          "flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-destructive"
        }
      >
        <ExclamationTriangleIcon className="size-4 shrink-0" />
        <span>{what} could not be loaded — unknown, not empty.</span>
        <span className="break-all font-mono text-muted-foreground">
          {reason}
        </span>
        <Button size="xs" variant="outline" onClick={onRetry}>
          Retry
        </Button>
      </div>
    );
  }
  return (
    <div
      role="alert"
      className={className ?? "flex flex-col items-center gap-3"}
    >
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
