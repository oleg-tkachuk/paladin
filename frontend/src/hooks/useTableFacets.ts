"use client";

import { useCallback, useMemo } from "react";

import { useUrlState, type UrlCodec } from "@/hooks/useUrlState";
import {
  activeFacets,
  chosen,
  countedOptions,
  countOptions,
  facetUpdates,
  filterByFacets,
  initialSelection,
  parseFacets,
  type CountedOption,
  type Facet,
  type FacetCounts,
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
 * The choices made in each facet, kept in the URL when `url` is given, and
 * the setter of one facet's choices. `facets` and `url` are module-level
 * constants, so the codec keeps its identity between renders.
 */
function useFacetSelection<R>(facets: readonly Facet<R>[], url?: TableUrl) {
  const codec = useMemo(
    () => (url ? facetCodec(facets, url) : null),
    [facets, url],
  );
  const [selection, setSelection] = useUrlState(
    codec,
    initialSelection(facets),
  );
  const choose = useCallback(
    (id: string, values: readonly string[]) =>
      setSelection((prev) => ({ ...prev, [id]: values })),
    [setSelection],
  );
  // Every facet emptied, not put back to its initial choice: "clear" that
  // left the list narrowed would not be one.
  const clear = useCallback(
    () => setSelection(Object.fromEntries(facets.map((f) => [f.id, []]))),
    [facets, setSelection],
  );
  return { selection, choose, clear };
}

/** The toolbar's controls: each facet, its answers counted by `options`. */
function controlsOf<R>(
  facets: readonly Facet<R>[],
  selection: FacetSelection,
  choose: (id: string, values: readonly string[]) => void,
  options: (f: Facet<R>, selected: readonly string[]) => CountedOption[],
): FacetControl[] {
  return facets.map((f) => {
    const selected = chosen(selection, f.id);
    return {
      id: f.id,
      label: f.label,
      options: options(f, selected),
      selected,
      onChange: (values) => choose(f.id, values),
    };
  });
}

/**
 * The facets of a fully loaded list: the choices made in each, kept in the
 * URL when `url` is given, the rows that pass them, and the controls the
 * toolbar shows, each answer counted over the rows given — the loaded set, so
 * the counts say what choosing an answer would leave.
 */
export function useTableFacets<R>(
  rows: readonly R[],
  facets: readonly Facet<R>[],
  url?: TableUrl,
) {
  const { selection, choose, clear } = useFacetSelection(facets, url);
  const kept = useMemo(
    () => filterByFacets(rows, facets, selection),
    [rows, facets, selection],
  );
  const controls = useMemo(
    () =>
      controlsOf(facets, selection, choose, (f, selected) =>
        countOptions(rows, f, selected),
      ),
    [rows, facets, selection, choose],
  );
  return {
    rows: kept,
    selection,
    controls,
    active: activeFacets(selection),
    clear,
  };
}

/**
 * The facets of a list the server pages: the choices, which the caller hands
 * the server to narrow by, and the toolbar's controls for the counts its
 * answer then gives — each answer counted by the server over the whole
 * listing. The choices come before the answer, so the controls are a
 * function of its counts. The rows are the server's to narrow; none are
 * filtered here.
 */
export function useServerFacets<R>(
  facets: readonly Facet<R>[],
  url?: TableUrl,
) {
  const { selection, choose, clear } = useFacetSelection(facets, url);
  const controls = useCallback(
    (counts: FacetCounts) =>
      controlsOf(facets, selection, choose, (f, selected) =>
        countedOptions(f, counts.get(f.id) ?? NO_COUNTS, selected),
      ),
    [facets, selection, choose],
  );
  return { selection, controls, active: activeFacets(selection), clear };
}

const NO_COUNTS: ReadonlyMap<string, number> = new Map();
