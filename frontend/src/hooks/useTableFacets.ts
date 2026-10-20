"use client";

import { useCallback, useMemo } from "react";

import { useUrlState, type UrlCodec } from "@/hooks/useUrlState";
import {
  activeFacets,
  chosen,
  countOptions,
  facetUpdates,
  filterByFacets,
  initialSelection,
  parseFacets,
  type CountedOption,
  type Facet,
  type FacetSelection,
} from "@/lib/table/facets";
import type { TableUrl } from "@/lib/table/urlState";

/** How a table's facet choices read from and write to the URL. */
function facetCodec<R>(
  facets: readonly Facet<R>[],
  url: TableUrl,
): UrlCodec<FacetSelection> {
  return {
    read: (params) => parseFacets(params, facets, url),
    write: (selection) => facetUpdates(selection, facets, url),
  };
}

/** One facet as the toolbar offers it: its choices, counted, and a setter. */
export interface FacetControl {
  id: string;
  label: string;
  options: readonly CountedOption[];
  selected: readonly string[];
  onChange: (values: readonly string[]) => void;
}

/**
 * The facets of a fully loaded list: the choices made in each, kept in the
 * URL when `url` is given, the rows that pass them, and the controls the
 * toolbar shows, each answer counted over the rows given — the loaded set, so
 * the counts say what choosing an answer would leave. `facets` and `url` are
 * module-level constants, so the codec keeps its identity between renders.
 */
export function useTableFacets<R>(
  rows: readonly R[],
  facets: readonly Facet<R>[],
  url?: TableUrl,
) {
  const codec = useMemo(
    () => (url ? facetCodec(facets, url) : null),
    [facets, url],
  );
  const [selection, setSelection] = useUrlState(
    codec,
    initialSelection(facets),
  );
  const kept = useMemo(
    () => filterByFacets(rows, facets, selection),
    [rows, facets, selection],
  );
  const controls = useMemo<FacetControl[]>(
    () =>
      facets.map((f) => {
        const selected = chosen(selection, f.id);
        return {
          id: f.id,
          label: f.label,
          options: countOptions(rows, f, selected),
          selected,
          onChange: (values) =>
            setSelection((prev) => ({ ...prev, [f.id]: values })),
        };
      }),
    [rows, facets, selection, setSelection],
  );
  // Every facet emptied, not put back to its initial choice: "clear" that
  // left the list narrowed would not be one.
  const clear = useCallback(
    () => setSelection(Object.fromEntries(facets.map((f) => [f.id, []]))),
    [facets, setSelection],
  );
  return {
    rows: kept,
    selection,
    controls,
    active: activeFacets(selection),
    clear,
  };
}
