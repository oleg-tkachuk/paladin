// How a list is laid out, after Argo CD's applications list: rows in a
// table, a tile per item, or a summary of how the items split. The choice is
// kept in the address bar, so a link opens the list as it was, and in the
// user's table preferences, so the list opens the way they left it.
import type { UrlCodec } from "@/hooks/useUrlState";
import { choiceUpdate, parseChoice } from "@/lib/table/urlState";

export const VIEW_MODES = ["table", "tiles", "summary"] as const;
export type ViewMode = (typeof VIEW_MODES)[number];
export const DEFAULT_VIEW_MODE: ViewMode = "table";

/** The query-string parameter the view is kept under. */
const VIEW_PARAM = "view";

/**
 * How the view reads from and writes to the URL. `fallback` is what a URL
 * without the parameter shows — the view the user saved — and is left out
 * of the URL, as a table's default sort is.
 */
export function viewModeCodec(fallback: ViewMode): UrlCodec<ViewMode> {
  return {
    read: (params) => parseChoice(params.get(VIEW_PARAM), VIEW_MODES, fallback),
    write: (view) => choiceUpdate(view, fallback, VIEW_PARAM),
  };
}

/** One value and how many items have it, as a summary lists it. */
export interface Tally {
  key: string;
  count: number;
}

/**
 * How many items have each value, most first, ties in the order of the
 * key. An item with no value is counted under `none`.
 */
export function tallyBy<R>(
  rows: readonly R[],
  keyOf: (row: R) => string | undefined,
  none: string,
): Tally[] {
  const counts = new Map<string, number>();
  for (const row of rows) {
    const key = keyOf(row) || none;
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return [...counts]
    .map(([key, count]) => ({ key, count }))
    .sort((a, b) => b.count - a.count || a.key.localeCompare(b.key));
}
