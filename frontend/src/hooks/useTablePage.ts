"use client";

import { useMemo, useState } from "react";

import { usePreferenceSnapshot } from "@/hooks/usePreference";
import { useUrlState, type UrlCodec } from "@/hooks/useUrlState";
import { PREFERENCE_KEYS } from "@/lib/preferences/keys";
import { readTablePrefs } from "@/lib/preferences/tablePrefs";
import {
  clampPage,
  pageCount,
  pageOf,
  pageSlice,
  parsePage,
  readPageSize,
  type PageSize,
} from "@/lib/table/paging";
import { tableParam, type TableUrl } from "@/lib/table/urlState";

// One codec per table namespace, so useUrlState sees the same object on
// every render.
const codecs = new Map<string, UrlCodec<number>>();
function pageCodec(url: TableUrl): UrlCodec<number> {
  const key = tableParam("page", url);
  let codec = codecs.get(key);
  if (!codec) {
    codec = {
      read: (params) => parsePage(params.get(key)),
      write: (page) => ({ [key]: page > 1 ? String(page) : null }),
    };
    codecs.set(key, codec);
  }
  return codec;
}

/** What TablePagination takes: where the list is, and how to move it. */
export interface Pagination {
  page: number;
  pageCount: number;
  pageSize: PageSize;
  total: number;
  /** The first and last row on the page, from 1: the "1–50" of "1–50 of 312". */
  first: number;
  last: number;
  onPage: (page: number) => void;
  onPageSize: (size: PageSize) => void;
}

/**
 * The page of a fully loaded list on screen. The page is kept in the URL when
 * `url` is given; the size is the one the user saved for `tableId`
 * (TablePagination saves a new one), read once as the sort is. A change to
 * anything in `resetOn` — the search, the facets — goes back to the first
 * page, since the page the reader was on may no longer exist. With `focus`,
 * the list moves to the page holding the row a link points at
 * (useLinkedRow), once for each row it is pointed at.
 */
export function useTablePage<R>(
  rows: readonly R[],
  {
    url,
    tableId,
    resetOn = [],
    focus,
  }: {
    /** Where the page lives in the URL; a module-level constant. */
    url?: TableUrl;
    /** The table's id in the user's preferences, for its page size. */
    tableId?: string;
    resetOn?: readonly unknown[];
    focus?: { id: string | null; key: (row: R) => string };
  } = {},
): { rows: readonly R[]; pagination: Pagination } {
  const stored = usePreferenceSnapshot(PREFERENCE_KEYS.tables);
  const [pageSize, setPageSize] = useState<PageSize>(() =>
    readPageSize(tableId ? readTablePrefs(stored, tableId).pageSize : null),
  );
  const [page, setPage] = useUrlState(url ? pageCodec(url) : null, 1);

  // Back to the first page when what the list holds changes; React's way of
  // adjusting state to a prop, without an effect that would paint the old
  // page first.
  const resetKey = JSON.stringify(resetOn);
  const [seenKey, setSeenKey] = useState(resetKey);
  if (seenKey !== resetKey) {
    setSeenKey(resetKey);
    setPage(1);
  }

  // The page holding a linked row, once the rows that hold it have loaded.
  const [jumpedTo, setJumpedTo] = useState<string | null>(null);
  if (focus?.id && focus.id !== jumpedTo) {
    const index = rows.findIndex((r) => focus.key(r) === focus.id);
    if (index >= 0) {
      setJumpedTo(focus.id);
      setPage(pageOf(index, pageSize));
    }
  }

  const count = pageCount(rows.length, pageSize);
  const current = clampPage(page, count);
  const slice = useMemo(
    () => pageSlice(rows, current, pageSize),
    [rows, current, pageSize],
  );
  return {
    rows: slice.rows,
    pagination: {
      page: current,
      pageCount: count,
      pageSize,
      total: rows.length,
      first: slice.first,
      last: slice.last,
      onPage: (p) => setPage(clampPage(p, count)),
      onPageSize: (size) => {
        setPageSize(size);
        setPage(1);
      },
    },
  };
}
