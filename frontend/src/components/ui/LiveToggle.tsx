"use client";

import { Button } from "@/components/ui/button";
import { LIVE_DOT_CLASS, liveTitle, type LiveState } from "@/lib/live";
import { cn } from "@/lib/utils";

/**
 * The one switch every refreshing view carries: on, it streams or polls;
 * off, the view holds still and says it is paused. The dot and the title
 * say which, from liveState.
 */
export function LiveToggle({
  state,
  onToggle,
  intervalMs,
}: {
  state: LiveState;
  onToggle: () => void;
  /** The polling interval, for a view that polls. */
  intervalMs?: number;
}) {
  const on = state !== "paused";
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={onToggle}
      aria-pressed={on}
      // A toggle keeps its name; aria-pressed says which way it is.
      aria-label="Live"
      title={liveTitle(state, intervalMs)}
    >
      <span className={cn("size-2 rounded-full", LIVE_DOT_CLASS[state])} />
      {on ? "Live" : "Paused"}
    </Button>
  );
}
