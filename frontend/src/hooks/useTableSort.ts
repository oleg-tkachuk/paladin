"use client";

import { useCallback, useMemo, useState } from "react";

import { sortCodec } from "@/hooks/useTableUrl";
import { useUrlState } from "@/hooks/useUrlState";
import type { TableUrl } from "@/lib/table/urlState";

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

const NO_SORT = { column: null, direction: null } as const;

/** Keeps a table's sort in the URL, among the columns it sorts by. */
export interface SortInUrl<C extends string> {
  url: TableUrl;
  columns: readonly C[];
}

/**
 * useTableSort wires nextSort into component state. `toggleSort(column)`
 * advances the cycle; `setSort` is exposed for the rare caller that needs to
 * set state directly. With `inUrl` the sort is kept in the query string, and a
 * URL naming a column the table does not sort by reads as the default.
 */
export function useTableSort<C extends string = string>(
  initial: SortState<C> = NO_SORT,
  inUrl?: SortInUrl<C>,
) {
  const url = inUrl?.url;
  const columns = inUrl?.columns;
  // Read once, as useState reads an initial value.
  const [fallback] = useState(initial);
  const codec = useMemo(
    () => (url && columns ? sortCodec(url, columns, fallback) : null),
    [url, columns, fallback],
  );
  const [sort, setSort] = useUrlState<SortState<C>>(codec, initial);
  const toggleSort = useCallback(
    (column: C) => setSort((prev) => nextSort(prev, column)),
    [setSort],
  );
  return { sort, toggleSort, setSort };
}
