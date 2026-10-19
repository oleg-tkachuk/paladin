// What a refreshing view says about itself: streaming, polling, trying to
// reconnect, or paused. Pure, so every Live control reads alike and the
// words are tested once.

/** How a view is kept current right now. */
export type LiveState = "stream" | "poll" | "reconnecting" | "paused";

const MS_PER_SECOND = 1000;

/**
 * The state of a view: paused when switched off; otherwise streaming, or
 * reconnecting while its stream is down; polling when it has no stream.
 */
export function liveState(opts: {
  live: boolean;
  /** Set for a view fed by a stream: whether it is connected. */
  connected?: boolean;
}): LiveState {
  if (!opts.live) return "paused";
  if (opts.connected === undefined) return "poll";
  return opts.connected ? "stream" : "reconnecting";
}

/** What the control says, for its tooltip and its accessible description. */
export function liveTitle(state: LiveState, intervalMs?: number): string {
  switch (state) {
    case "stream":
      return "Live — new entries appear as they happen";
    case "reconnecting":
      return "Live — reconnecting…";
    case "poll":
      return intervalMs
        ? `Live — refreshes every ${Math.round(intervalMs / MS_PER_SECOND)} s while this tab is open`
        : "Live — refreshes while this tab is open";
    case "paused":
      return "Paused — refresh by hand, or switch Live on";
  }
}

/** The dot's colour and motion, by state. */
export const LIVE_DOT_CLASS: Record<LiveState, string> = {
  stream: "bg-success animate-pulse",
  poll: "bg-success animate-pulse",
  reconnecting: "bg-warning",
  paused: "bg-muted-foreground/40",
};
