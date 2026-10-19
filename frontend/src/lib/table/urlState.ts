// How a table's view lives in the address bar: the search, the sort and the
// one choice its toolbar filters by. Pure, so every list reads and writes the
// same parameters the same way, and a link to a filtered list opens it as it
// was. A value the URL cannot hold for this table — a column it does not sort
// by, a choice it does not offer — reads as the default rather than failing.
import type { SortDirection, SortState } from "@/hooks/useTableSort";

/** The query-string parameters a table's view is kept under. */
export const TABLE_PARAMS = {
  query: "q",
  sort: "sort",
  dir: "dir",
  filter: "filter",
} as const;

export type TableParam = keyof typeof TABLE_PARAMS;

/** Joins a namespace to a parameter name, as in `grants.q`. */
const NAMESPACE_SEPARATOR = ".";

/**
 * Where a table keeps its view in the URL. The page's own list takes the bare
 * names; a table embedded beside others takes a namespace so the two do not
 * read each other's search. Declared once at module level, so the hooks that
 * take it see the same object on every render.
 */
export interface TableUrl {
  namespace?: string;
}

/** The page's main list: bare parameter names. */
export const PAGE_TABLE: TableUrl = {};

/** The name a table keeps one part of its view under. */
export function tableParam(name: TableParam, url: TableUrl): string {
  const base = TABLE_PARAMS[name];
  return url.namespace ? `${url.namespace}${NAMESPACE_SEPARATOR}${base}` : base;
}

/** The URL parameters a value sets, by name; null removes the parameter. */
export type ParamUpdates = Readonly<Record<string, string | null>>;

/**
 * The query string after the updates, leading "?" included, or "" when no
 * parameter is left. Parameters the updates do not name keep their place.
 */
export function withParams(search: string, updates: ParamUpdates): string {
  const params = new URLSearchParams(search);
  for (const [key, value] of Object.entries(updates)) {
    if (value === null || value === "") params.delete(key);
    else params.set(key, value);
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

const DIRECTIONS: readonly Exclude<SortDirection, null>[] = ["asc", "desc"];

/**
 * The sort a URL names, when this table sorts by that column in that
 * direction; otherwise the table's default. An old link may name a column
 * since removed.
 */
export function parseSort<C extends string>(
  params: URLSearchParams,
  url: TableUrl,
  columns: readonly C[],
  fallback: SortState<C>,
): SortState<C> {
  const column = params.get(tableParam("sort", url));
  const dir = params.get(tableParam("dir", url));
  const known = columns.find((c) => c === column);
  const direction = DIRECTIONS.find((d) => d === dir);
  return known && direction ? { column: known, direction } : fallback;
}

/**
 * The parameters a sort sets. No sort, or the table's default, removes both:
 * a URL without a sort is the default.
 */
export function sortUpdates<C extends string>(
  sort: SortState<C>,
  url: TableUrl,
  fallback: SortState<C>,
): ParamUpdates {
  const isDefault =
    sort.column === fallback.column && sort.direction === fallback.direction;
  const set = !isDefault && sort.column !== null && sort.direction !== null;
  return {
    [tableParam("sort", url)]: set ? sort.column : null,
    [tableParam("dir", url)]: set ? sort.direction : null,
  };
}

/** The choice a URL names, when the table offers it; otherwise the fallback. */
export function parseChoice<V extends string>(
  raw: string | null,
  options: readonly V[],
  fallback: V,
): V {
  return options.find((o) => o === raw) ?? fallback;
}

/** The parameter a choice sets; the default is left out of the URL. */
export function choiceUpdate<V extends string>(
  value: V,
  fallback: V,
  key: string,
): ParamUpdates {
  return { [key]: value === fallback ? null : value };
}
