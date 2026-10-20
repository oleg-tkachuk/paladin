// How a fully loaded list is cut into pages, after Argo CD's: a page size the
// user picks per table, and the page the list is on. Pure, so the arithmetic
// of the first, the last and an out-of-range page is tested once.

/** Rows per page the size picker offers; "all" shows the whole list. */
export const PAGE_SIZES = [10, 25, 50, 100, "all"] as const;
export type PageSize = (typeof PAGE_SIZES)[number];

/**
 * What a table shows until its user picks: enough that most lists still fit
 * on one page, as they all did before pages, and few enough that a list of
 * hundreds stays quick to render and to scan.
 */
export const DEFAULT_PAGE_SIZE: PageSize = 50;

/** A stored size, when it is one the picker offers; otherwise the default. */
export function readPageSize(raw: unknown): PageSize {
  return PAGE_SIZES.find((s) => s === raw) ?? DEFAULT_PAGE_SIZE;
}

/** How many pages `total` rows take; one at least, so page 1 always exists. */
export function pageCount(total: number, size: PageSize): number {
  if (size === "all" || total === 0) return 1;
  return Math.ceil(total / size);
}

/** A page number brought into 1…count: a link may name a page since emptied. */
export function clampPage(page: number, count: number): number {
  if (!Number.isFinite(page) || page < 1) return 1;
  return Math.min(Math.floor(page), count);
}

/** The page (from 1) the row at `index` (from 0) falls on. */
export function pageOf(index: number, size: PageSize): number {
  return size === "all" ? 1 : Math.floor(index / size) + 1;
}

/**
 * The rows of one page, and where they sit in the whole, from 1: the "1–50"
 * of "1–50 of 312". An empty list is the range 0–0.
 */
export function pageSlice<R>(
  rows: readonly R[],
  page: number,
  size: PageSize,
): { rows: readonly R[]; first: number; last: number } {
  if (size === "all") {
    return { rows, first: rows.length ? 1 : 0, last: rows.length };
  }
  const start = (page - 1) * size;
  const shown = rows.slice(start, start + size);
  return {
    rows: shown,
    first: shown.length ? start + 1 : 0,
    last: start + shown.length,
  };
}

/** The page a URL names; absent or unreadable is the first. */
export function parsePage(raw: string | null): number {
  const n = Number(raw);
  return raw && Number.isInteger(n) && n > 0 ? n : 1;
}
