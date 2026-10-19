"use client";

import { useRef } from "react";
import {
  useQueryClient,
  type FetchQueryOptions,
  type QueryKey,
} from "@tanstack/react-query";

/**
 * Warms a detail page's query when the pointer rests on its row or the row
 * takes focus — the moment the user signals they are going there — so the
 * page opens on data instead of a skeleton. Once per row; the query client
 * does nothing for data it already holds fresh.
 */
export function usePrefetchOnIntent<
  TQueryFnData,
  TError,
  TData,
  TQueryKey extends QueryKey,
>(options: FetchQueryOptions<TQueryFnData, TError, TData, TQueryKey>) {
  const qc = useQueryClient();
  const done = useRef(false);
  const warm = () => {
    if (done.current) return;
    done.current = true;
    void qc.prefetchQuery(options);
  };
  return { onMouseEnter: warm, onFocus: warm };
}
