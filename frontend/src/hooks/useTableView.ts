"use client";

import { useMemo } from "react";

import { useTableSort, type SortState } from "@/hooks/useTableSort";
import { useTableQuery } from "@/hooks/useTableUrl";
import {
  filterRows,
  sortRows,
  type SearchText,
  type SortAccessors,
} from "@/lib/table/view";
import type { TableUrl } from "@/lib/table/urlState";

/**
 * The search and sort of a fully loaded table, in one place: the query the
 * toolbar edits, the sort the column headers cycle, and the rows that result.
 * A table that pages on the server keeps its query on the server and uses
 * useTableSort alone. With `url` the search and sort are kept in the query
 * string, so a reload or a shared link opens the list as it was.
 */
export function useTableView<R, C extends string>(
  rows: readonly R[],
  {
    text,
    accessors,
    initialSort,
    url,
    tableId,
  }: {
    /** The fields a search looks in. */
    text: SearchText<R>;
    /** How each sortable column reads its value. */
    accessors: SortAccessors<R, C>;
    initialSort?: SortState<C>;
    /** Where the view lives in the URL; a module-level constant. */
    url?: TableUrl;
    /** The table's id in the user's preferences, for its default sort. */
    tableId?: string;
  },
) {
  const [query, setQuery] = useTableQuery(url);
  const columns = useMemo(() => Object.keys(accessors) as C[], [accessors]);
  const { sort, toggleSort } = useTableSort<C>(
    initialSort,
    url ? { url, columns, tableId } : undefined,
  );
  const shown = useMemo(
    () => sortRows(filterRows(rows, query, text), sort, accessors),
    [rows, query, text, sort, accessors],
  );
  return { query, setQuery, sort, toggleSort, rows: shown, total: rows.length };
}
