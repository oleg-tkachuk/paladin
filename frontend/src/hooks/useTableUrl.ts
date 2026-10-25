"use client";

import { useMemo } from "react";

import { useUrlState, type UrlCodec } from "@/hooks/useUrlState";
import type { SortState } from "@/hooks/useTableSort";
import {
  parseSort,
  sortUpdates,
  tableParam,
  type TableUrl,
} from "@/lib/table/urlState";

/** How a table's search reads from and writes to the URL. */
export function queryCodec(url: TableUrl): UrlCodec<string> {
  const key = tableParam("query", url);
  return {
    read: (params) => params.get(key) ?? "",
    write: (query) => ({ [key]: query }),
  };
}

/** How a table's sort reads from and writes to the URL. */
export function sortCodec<C extends string>(
  url: TableUrl,
  columns: readonly C[],
  fallback: SortState<C>,
): UrlCodec<SortState<C>> {
  return {
    read: (params) => parseSort(params, url, columns, fallback),
    write: (sort) => sortUpdates(sort, url, fallback),
  };
}

// The hooks here take their TableUrl and options as module-level constants,
// so the codec they build keeps its identity between renders.

/**
 * A table's search box, kept in the URL when `url` is given. For a list that
 * searches on the server; a fully loaded one has it from useTableView.
 */
export function useTableQuery(url?: TableUrl) {
  const codec = useMemo(() => (url ? queryCodec(url) : null), [url]);
  return useUrlState(codec, "");
}
