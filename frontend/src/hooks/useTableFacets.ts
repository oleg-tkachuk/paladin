"use client";

import {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { QueryClientContext } from "@tanstack/react-query";

import { useUrlState, type UrlCodec } from "@/hooks/useUrlState";
import { updatePreference, usePreferenceSnapshot } from "@/hooks/usePreference";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import { readTablePrefs, withTablePrefs } from "@/lib/preferences/tablePrefs";
import {
  activeFacets,
  chosen,
  countedOptions,
  countOptions,
  FACET_COUNTING,
  facetUpdates,
  filterByFacets,
  initialSelection,
  openingSelection,
  rememberedSelection,
  type CountedOption,
  type Facet,
  type FacetCounting,
  type FacetCounts,
  type FacetSelection,
} from "@/lib/table/facets";
import type { TableUrl } from "@/lib/table/urlState";

/** How a table's facet choices read from and write to the URL. */
function facetCodec<R>(
  facets: readonly Facet<R>[],
  url: TableUrl,
  saved: FacetSelection | undefined,
): UrlCodec<FacetSelection> {
  return {
    read: (params) => openingSelection(params, facets, url, saved),
    write: (selection) => facetUpdates(selection, facets, url),
  };
}

/**
 * Facet choices saved for the next visit, as the toolbar shows them: that
 * they are on, and how to clear them with the choices on screen.
 */
export interface SavedFacets {
  clear: () => void;
}

/** One facet as the toolbar offers it: its choices, counted, and a setter. */
export interface FacetControl {
  id: string;
  label: string;
  options: readonly CountedOption[];
  selected: readonly string[];
  onChange: (values: readonly string[]) => void;
  /** How the options' counts read; exact when absent. */
  counting?: FacetCounting;
}

/**
 * The choices made in each facet, kept in the URL when `url` is given, and
 * the setter of one facet's choices. `facets` and `url` are module-level
 * constants, so the codec keeps its identity between renders.
 *
 * With `tableId` as well, the choices the user makes are saved in the
 * table's preferences (console.tables) and a later visit whose URL names no
 * facet opens on them (openingSelection). Only a choice made here is saved,
 * never one a link brought; clearing clears the saved ones too. The saved
 * choices are read once, as the saved sort is.
 */
function useFacetSelection<R>(
  facets: readonly Facet<R>[],
  url?: TableUrl,
  tableId?: string,
) {
  const qc = useContext(QueryClientContext);
  const stored = usePreferenceSnapshot(PREFERENCE_KEYS.tables);
  const remembers = Boolean(url && tableId && qc);
  const [saved, setSaved] = useState<FacetSelection | undefined>(() =>
    remembers && tableId ? readTablePrefs(stored, tableId).facets : undefined,
  );
  const codec = useMemo(
    () => (url ? facetCodec(facets, url, saved) : null),
    [facets, url, saved],
  );
  const [selection, setSelection] = useUrlState(
    codec,
    initialSelection(facets),
  );

  const remember = useCallback(
    (next: Record<string, string[]> | undefined) => {
      if (!remembers || !qc || !tableId) return;
      setSaved(next);
      updatePreference(
        qc,
        PREFERENCE_KEYS.tables,
        // A map of JSON values, which the schema read it through.
        (raw) =>
          withTablePrefs(raw, tableId, (p) => ({
            ...p,
            facets: next,
          })) as never,
      ).catch((err: unknown) =>
        // The choice stands on screen; only the next visit misses it.
        console.warn(`saving the facet choices of ${tableId} failed`, err),
      );
    },
    [remembers, qc, tableId],
  );

  // The choices as the last change left them, ahead of the render it
  // causes: a saved view sets several facets in one go, and each change
  // builds on the one before, as a state updater would.
  const latest = useRef(selection);
  useEffect(() => {
    latest.current = selection;
  }, [selection]);
  const choose = useCallback(
    (id: string, values: readonly string[]) => {
      const next = { ...latest.current, [id]: values };
      latest.current = next;
      setSelection(next);
      remember(rememberedSelection(facets, next));
    },
    [setSelection, remember, facets],
  );
  // Every facet emptied, not put back to its initial choice: "clear" that
  // left the list narrowed would not be one. What was saved goes with it, so
  // the next visit opens where a first one would.
  const clear = useCallback(() => {
    const none = Object.fromEntries(facets.map((f) => [f.id, []]));
    latest.current = none;
    setSelection(none);
    remember(undefined);
  }, [facets, setSelection, remember]);
  const savedFacets = useMemo<SavedFacets | null>(
    () => (saved ? { clear } : null),
    [saved, clear],
  );
  return { selection, choose, clear, saved: savedFacets };
}

/** The toolbar's controls: each facet, its answers counted by `options`. */
function controlsOf<R>(
  facets: readonly Facet<R>[],
  selection: FacetSelection,
  choose: (id: string, values: readonly string[]) => void,
  options: (f: Facet<R>, selected: readonly string[]) => CountedOption[],
  counting: FacetCounting = FACET_COUNTING.exact,
): FacetControl[] {
  return facets.map((f) => {
    const selected = chosen(selection, f.id);
    return {
      id: f.id,
      label: f.label,
      options: options(f, selected),
      selected,
      onChange: (values) => choose(f.id, values),
      counting,
    };
  });
}

/**
 * The facets of a fully loaded list: the choices made in each, kept in the
 * URL when `url` is given, the rows that pass them, and the controls the
 * toolbar shows, each answer counted over the rows given — the loaded set, so
 * the counts say what choosing an answer would leave. With `tableId` too,
 * the choices are remembered between visits (useFacetSelection).
 */
export function useTableFacets<R>(
  rows: readonly R[],
  facets: readonly Facet<R>[],
  url?: TableUrl,
  tableId?: string,
) {
  const { selection, choose, clear, saved } = useFacetSelection(
    facets,
    url,
    tableId,
  );
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
    saved,
  };
}

/**
 * The facets of a list the server pages: the choices, which the caller hands
 * the server to narrow by, and the toolbar's controls for the counts its
 * answer then gives — each answer counted by the server over the whole
 * listing. The choices come before the answer, so the controls are a
 * function of its counts, and of how they read: a server that capped its
 * scan gives lower bounds, one that withheld them gives none. The rows are
 * the server's to narrow; none are filtered here. `choose` sets one facet's
 * choices outright, as a saved view does. With `tableId` too, the choices
 * are remembered between visits (useFacetSelection).
 */
export function useServerFacets<R>(
  facets: readonly Facet<R>[],
  url?: TableUrl,
  tableId?: string,
) {
  const { selection, choose, clear, saved } = useFacetSelection(
    facets,
    url,
    tableId,
  );
  const controls = useCallback(
    (counts: FacetCounts, counting: FacetCounting = FACET_COUNTING.exact) =>
      controlsOf(
        facets,
        selection,
        choose,
        (f, selected) =>
          countedOptions(f, counts.get(f.id) ?? NO_COUNTS, selected),
        counting,
      ),
    [facets, selection, choose],
  );
  return {
    selection,
    controls,
    choose,
    active: activeFacets(selection),
    clear,
    saved,
  };
}

const NO_COUNTS: ReadonlyMap<string, number> = new Map();
