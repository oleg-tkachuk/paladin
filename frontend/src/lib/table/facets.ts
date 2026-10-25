// A list's facets, after Argo CD's filter sidebar: each a question a row
// answers (its state, its health), offered as a list of choices with the
// number of loaded rows that answer each way. Choices within a facet widen the
// list — a row answering any of them stays — and facets narrow it, since a row
// must pass every one. Pure, so the counting, the filtering and the form the
// choices take in the URL are tested once.
import type { StatusTone } from "@/lib/ui/status";

import { namespacedParam, type ParamUpdates, type TableUrl } from "./urlState";

/** One answer a facet offers. */
export interface FacetOption {
  value: string;
  label: string;
  /** The status tone the answer carries in the rows, shown as its dot. */
  tone?: StatusTone;
}

/** One question a list's rows answer. Declared once, at module level. */
export interface Facet<R> {
  /** The facet's key, and the URL parameter its choices are kept under. */
  id: string;
  /** Names the facet on its button and in its menu: "State". */
  label: string;
  /**
   * The answers a row gives. Most rows give one; a backend can be draining
   * and in maintenance at once, and a tenant carries several labels.
   */
  values: (row: R) => readonly string[];
  /**
   * A fixed vocabulary, in the order the menu lists it; a URL naming any
   * other value reads as no choice. Without one, the menu lists the answers
   * the loaded rows give, in order, and the URL may name any.
   */
  options?: readonly FacetOption[];
  /**
   * Names an answer the facet has no fixed vocabulary for; the answer
   * itself when absent.
   */
  optionLabel?: (value: string) => string;
  /**
   * What the list shows before anyone chooses — the tokens still in use, not
   * the revoked — so a fresh visit is not a wall of dead rows.
   */
  initial?: readonly string[];
}

/** The choices made in each facet, by facet id; none is no narrowing. */
export type FacetSelection = Readonly<Record<string, readonly string[]>>;

/** An answer as the menu offers it: with how many loaded rows give it. */
export interface CountedOption extends FacetOption {
  count: number;
}

/** Separates the choices of one facet in its URL parameter. */
const VALUE_SEPARATOR = ",";

/**
 * Stands in the URL for "no choice" in a facet whose list opens narrowed: the
 * absent parameter already means the initial choice, so clearing it has to
 * say something.
 */
const FACET_ANY = "any";

const NO_CHOICE: readonly string[] = [];

/** Each facet at its initial choice. */
export function initialSelection<R>(
  facets: readonly Facet<R>[],
): FacetSelection {
  return Object.fromEntries(facets.map((f) => [f.id, f.initial ?? NO_CHOICE]));
}

/** The choices made in one facet; none when the selection omits it. */
export function chosen(selection: FacetSelection, id: string) {
  return selection[id] ?? NO_CHOICE;
}

/** How many facets narrow the list. */
export function activeFacets(selection: FacetSelection): number {
  return Object.values(selection).filter((v) => v.length > 0).length;
}

/**
 * The rows that pass every facet with a choice made; the input as is when no
 * facet narrows it, so a list that filters nothing keeps its identity.
 */
export function filterByFacets<R>(
  rows: readonly R[],
  facets: readonly Facet<R>[],
  selection: FacetSelection,
): readonly R[] {
  const narrowing = facets
    .map((f) => ({ facet: f, keep: new Set(chosen(selection, f.id)) }))
    .filter((n) => n.keep.size > 0);
  if (narrowing.length === 0) return rows;
  return rows.filter((row) =>
    narrowing.every(({ facet, keep }) =>
      facet.values(row).some((v) => keep.has(v)),
    ),
  );
}

/**
 * Each answer the menu offers, with how many of `rows` give it. A row giving
 * the same answer twice counts once. A chosen answer no row gives is still
 * offered, at nought, so it can be unchosen.
 */
export function countOptions<R>(
  rows: readonly R[],
  facet: Facet<R>,
  selected: readonly string[],
): CountedOption[] {
  const counts = new Map<string, number>();
  for (const row of rows) {
    for (const v of new Set(facet.values(row))) {
      counts.set(v, (counts.get(v) ?? 0) + 1);
    }
  }
  return countedOptions(facet, counts, selected);
}

/**
 * Each answer the menu offers, with its count from `counts` — answers a
 * server counted over a list it pages, or the rows counted above. An answer
 * without a count is offered at nought; a chosen one is always offered, so
 * it can be unchosen.
 */
export function countedOptions<R>(
  facet: Facet<R>,
  counts: ReadonlyMap<string, number>,
  selected: readonly string[],
): CountedOption[] {
  const label = facet.optionLabel ?? ((value: string) => value);
  const offered: FacetOption[] = facet.options
    ? [...facet.options]
    : [...counts.keys()]
        .sort((a, b) => a.localeCompare(b, undefined, { numeric: true }))
        .map((value) => ({ value, label: label(value) }));
  for (const value of selected) {
    if (!offered.some((o) => o.value === value)) {
      offered.push({ value, label: label(value) });
    }
  }
  return offered.map((o) => ({ ...o, count: counts.get(o.value) ?? 0 }));
}

/** One count a server gave: how many rows give `value` for `facet`. */
export interface ServerFacetCount {
  facet: string;
  value: string;
  count: bigint | number;
}

/** A server's counts by facet id, then by answer. */
export type FacetCounts = ReadonlyMap<string, ReadonlyMap<string, number>>;

/** counts grouped by facet, each answer's value read through `value`. */
export function groupFacetCounts(
  counts: readonly ServerFacetCount[],
  value: (c: ServerFacetCount) => string = (c) => c.value,
): FacetCounts {
  const out = new Map<string, Map<string, number>>();
  for (const c of counts) {
    const byValue = out.get(c.facet) ?? new Map<string, number>();
    const v = value(c);
    byValue.set(v, (byValue.get(v) ?? 0) + Number(c.count));
    out.set(c.facet, byValue);
  }
  return out;
}

/** The URL parameter a facet's choices are kept under. */
function facetParam<R>(facet: Facet<R>, url: TableUrl): string {
  return namespacedParam(facet.id, url);
}

/** Whether two choices name the same answers, in any order. */
function sameChoice(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((v) => b.includes(v));
}

/**
 * One facet's choices as the URL names them. An absent parameter is the
 * initial choice; a value a fixed vocabulary does not hold is dropped, as an
 * old link may name an answer since removed.
 */
function parseFacet<R>(raw: string | null, facet: Facet<R>): readonly string[] {
  if (raw === null) return facet.initial ?? NO_CHOICE;
  if (raw === FACET_ANY && facet.initial?.length) return NO_CHOICE;
  const values = [...new Set(raw.split(VALUE_SEPARATOR))].filter(Boolean);
  if (!facet.options) return values;
  return facet.options.map((o) => o.value).filter((v) => values.includes(v));
}

/** Every facet's choices as the URL names them. */
export function parseFacets<R>(
  params: URLSearchParams,
  facets: readonly Facet<R>[],
  url: TableUrl,
): FacetSelection {
  return Object.fromEntries(
    facets.map((f) => [f.id, parseFacet(params.get(facetParam(f, url)), f)]),
  );
}

/**
 * The parameters a selection sets. A facet at its initial choice is left out
 * of the URL, as a default sort is.
 */
export function facetUpdates<R>(
  selection: FacetSelection,
  facets: readonly Facet<R>[],
  url: TableUrl,
): ParamUpdates {
  return Object.fromEntries(
    facets.map((f) => {
      const values = chosen(selection, f.id);
      const initial = f.initial ?? NO_CHOICE;
      let value: string | null;
      if (sameChoice(values, initial)) value = null;
      else if (values.length === 0) value = FACET_ANY;
      else value = values.join(VALUE_SEPARATOR);
      return [facetParam(f, url), value];
    }),
  );
}
