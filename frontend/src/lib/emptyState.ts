// Why a list shows nothing, and what it says about it. Pure, so every list
// tells the same three states apart in the same words.

/**
 * Why a list is empty: still searching (a debounce or a request not yet
 * answered), nothing matching what the user typed or chose, or nothing at
 * all. The first two must never read as the third — "no buckets yet" while
 * a search is in flight tells an operator their bucket is gone.
 */
export type EmptyReason = "pending" | "filtered" | "empty";

export function emptyReason(opts: {
  pending?: boolean;
  filtered?: boolean;
}): EmptyReason {
  if (opts.pending) return "pending";
  return opts.filtered ? "filtered" : "empty";
}

/** What the list says, for a plural noun in lower case: "buckets". */
export function emptyMessage(reason: EmptyReason, what: string): string {
  switch (reason) {
    case "pending":
      return "Searching…";
    case "filtered":
      return `No ${what} match your filters.`;
    case "empty":
      return `No ${what} yet.`;
  }
}
