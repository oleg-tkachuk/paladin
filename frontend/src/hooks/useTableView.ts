"use client";

import { useMemo, useState } from "react";

import { useTableSort, type SortState } from "@/hooks/useTableSort";
import {
  filterRows,
  sortRows,
  type SearchText,
  type SortAccessors,
} from "@/lib/table/view";

/**
 * The search and sort of a fully loaded table, in one place: the query the
 * toolbar edits, the sort the column headers cycle, and the rows that result.
 * A table that pages on the server keeps its query on the server and uses
 * useTableSort alone.
 */
export function useTableView<R, C extends string>(
  rows: readonly R[],
  {
    text,
    accessors,
    initialSort,
  }: {
    /** The fields a search looks in. */
    text: SearchText<R>;
    /** How each sortable column reads its value. */
    accessors: SortAccessors<R, C>;
    initialSort?: SortState<C>;
  },
) {
  const [query, setQuery] = useState("");
  const { sort, toggleSort } = useTableSort<C>(initialSort);
  const shown = useMemo(
    () => sortRows(filterRows(rows, query, text), sort, accessors),
    [rows, query, text, sort, accessors],
  );
  return { query, setQuery, sort, toggleSort, rows: shown, total: rows.length };
}
