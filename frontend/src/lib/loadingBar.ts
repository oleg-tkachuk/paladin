// When the top progress bar shows: while something loads for the first time
// or a change is being saved — never for a background refresh of data the
// page already shows, which would make it flicker with every poll.
import type { Query } from "@tanstack/react-query";

/** How long a load runs before the bar appears: a fast answer shows none. */
export const LOADING_BAR_DELAY_MS = 150;

/** A query loading data the page does not have yet. */
export function isFirstLoad(query: Query): boolean {
  return (
    query.state.status === "pending" && query.state.fetchStatus === "fetching"
  );
}
