// The client-side view of a fully loaded table: which rows a search keeps and
// in what order they show. Pure, so every table filters and sorts the same way
// and the rules are tested once. A server-paged table filters on the server
// instead and uses none of this.
import type { SortState } from "@/hooks/useTableSort";

/** A value a column sorts by. Absent values sort last in either direction. */
export type SortValue = string | number | bigint | Date | null | undefined;

/** How each sortable column reads its value off a row. */
export type SortAccessors<R, C extends string> = Record<
  C,
  (row: R) => SortValue
>;

/** The text of a row a search looks in: every searchable field, in a list. */
export type SearchText<R> = (row: R) => readonly (string | undefined)[];

/**
 * The rows whose searchable text contains every word of the query, ignoring
 * case. An empty query keeps every row, and the input array is returned as is.
 */
export function filterRows<R>(
  rows: readonly R[],
  query: string,
  text: SearchText<R>,
): readonly R[] {
  const words = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return rows;
  return rows.filter((row) => {
    const haystack = text(row)
      .filter((t): t is string => !!t)
      .join(" ")
      .toLocaleLowerCase();
    return words.every((w) => haystack.includes(w));
  });
}

const comparable = (v: SortValue): string | number | bigint | null => {
  if (v === null || v === undefined) return null;
  if (v instanceof Date) return v.getTime();
  return v;
};

/** Compares two present values: text by locale, numbers numerically. */
function compareValues(
  a: string | number | bigint,
  b: string | number | bigint,
): number {
  if (typeof a === "string" && typeof b === "string") {
    return a.localeCompare(b, undefined, {
      numeric: true,
      sensitivity: "base",
    });
  }
  if (a < b) return -1;
  return a > b ? 1 : 0;
}

/**
 * The rows in the order sort asks for, as a new array; rows that tie keep
 * their order. Unsorted returns the input as is, which is the order the API
 * gave.
 */
export function sortRows<R, C extends string>(
  rows: readonly R[],
  sort: SortState<C>,
  accessors: SortAccessors<R, C>,
): readonly R[] {
  if (sort.column === null || sort.direction === null) return rows;
  const read = accessors[sort.column];
  const sign = sort.direction === "asc" ? 1 : -1;
  return rows
    .map((row, index) => ({ row, index, value: comparable(read(row)) }))
    .sort((a, b) => {
      if (a.value === null || b.value === null) {
        if (a.value === b.value) return a.index - b.index;
        return a.value === null ? 1 : -1;
      }
      return sign * compareValues(a.value, b.value) || a.index - b.index;
    })
    .map((e) => e.row);
}
