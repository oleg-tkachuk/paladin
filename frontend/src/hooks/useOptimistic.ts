"use client";

import { QueryClientContext } from "@tanstack/react-query";
import { useCallback, useContext } from "react";

import { optimistically, type OptimisticEdit } from "@/lib/optimistic";

/**
 * optimistically() bound to the page's query client. A component rendered
 * without one — alone in a test — runs the change with nothing to edit.
 */
export function useOptimistic() {
  const qc = useContext(QueryClientContext);
  return useCallback(
    <T>(edits: readonly OptimisticEdit[], mutate: () => Promise<T>) =>
      qc ? optimistically(qc, edits, mutate) : mutate(),
    [qc],
  );
}
