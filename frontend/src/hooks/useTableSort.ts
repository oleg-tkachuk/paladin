"use client";

import { useCallback, useState } from "react";

/**
 * Shared table-sort state — the three-state column cycle that every list page
 * (buckets, collections, …) had hand-rolled as a local `SortDirection` /
 * `SortState` / `nextSort` triple. Extracted so the cycle lives in one
 * unit-tested place instead of drifting per page.
 *
 * The cycle, clicking the same column repeatedly:
 *   (unsorted) → asc → desc → unsorted → asc → …
 * Clicking a different column always starts it at `asc`.
 *
 * The hook owns only the STATE; the actual row comparison stays in the page
 * (it is data-shaped — `sort.direction === "asc" ? 1 : -1` over the page's
 * own fields).
 */
export type SortDirection = "asc" | "desc" | null;

export interface SortState<C extends string = string> {
  column: C | null;
  direction: SortDirection;
}

/**
 * nextSort is the pure reducer for the cycle above. Exported (and tested)
 * independently of the hook so the transition table can be asserted without a
 * React renderer.
 */
export function nextSort<C extends string>(
  prev: SortState<C>,
  column: C,
): SortState<C> {
  if (prev.column !== column) return { column, direction: "asc" };
  if (prev.direction === "asc") return { column, direction: "desc" };
  if (prev.direction === "desc") return { column: null, direction: null };
  return { column, direction: "asc" };
}

/**
 * useTableSort wires nextSort into component state. `toggleSort(column)`
 * advances the cycle; `setSort` is exposed for the rare caller that needs to
 * set state directly (e.g. restoring from a URL param).
 */
export function useTableSort<C extends string = string>(
  initial: SortState<C> = { column: null, direction: null },
) {
  const [sort, setSort] = useState<SortState<C>>(initial);
  const toggleSort = useCallback(
    (column: C) => setSort((prev) => nextSort(prev, column)),
    [],
  );
  return { sort, toggleSort, setSort };
}
