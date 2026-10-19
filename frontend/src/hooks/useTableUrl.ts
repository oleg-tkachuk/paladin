"use client";

import { useMemo } from "react";

import type { ToolbarFilterOption } from "@/components/ui/TableToolbar";
import { useUrlState, type UrlCodec } from "@/hooks/useUrlState";
import type { SortState } from "@/hooks/useTableSort";
import {
  choiceUpdate,
  parseChoice,
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

/** How a table's toolbar choice reads from and writes to the URL. */
export function choiceCodec<V extends string>(
  url: TableUrl,
  options: readonly V[],
  fallback: V,
): UrlCodec<V> {
  const key = tableParam("filter", url);
  return {
    read: (params) => parseChoice(params.get(key), options, fallback),
    write: (value) => choiceUpdate(value, fallback, key),
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

/**
 * The one choice a table's toolbar filters by, kept in the URL when `url` is
 * given; the default stays out of the URL.
 */
export function useTableFilter<V extends string>(
  options: readonly ToolbarFilterOption<V>[],
  fallback: V,
  url?: TableUrl,
) {
  const codec = useMemo(
    () =>
      url
        ? choiceCodec(
            url,
            options.map((o) => o.value),
            fallback,
          )
        : null,
    [url, options, fallback],
  );
  return useUrlState(codec, fallback);
}
