// A change shown before the server answers it: written into the cached
// queries at once, put back as it was when the server refuses, and read again
// from the server either way. One shape for every list and detail page whose
// result the console can predict — a toggle, a rename, a delete.
import type { QueryClient, QueryKey } from "@tanstack/react-query";

/** What a change does to the data cached under one key (and the keys below it). */
export interface OptimisticEdit<D = unknown> {
  queryKey: QueryKey;
  /** The data as the change leaves it; called once per cached query. */
  apply: (data: D) => D;
}

/** Builds an edit, typing `apply` by the data cached under the key. */
export function edit<D>(
  queryKey: QueryKey,
  apply: (data: D) => D,
): OptimisticEdit {
  return { queryKey, apply: apply as (data: unknown) => unknown };
}

/**
 * Applies the edits to the cache, then runs `mutate`. When it fails, every
 * query an edit touched is put back as it was before, and the error is
 * thrown on. Either way those queries are read again, so the cache ends as
 * the server has it.
 */
export async function optimistically<T>(
  qc: QueryClient,
  edits: readonly OptimisticEdit[],
  mutate: () => Promise<T>,
): Promise<T> {
  // A read in flight would land after the edit and overwrite it.
  await Promise.all(
    edits.map((e) => qc.cancelQueries({ queryKey: e.queryKey })),
  );
  const before = edits.flatMap((e) =>
    qc.getQueriesData({ queryKey: e.queryKey }),
  );
  for (const e of edits) {
    qc.setQueriesData({ queryKey: e.queryKey }, (data: unknown) =>
      data === undefined ? data : e.apply(data),
    );
  }
  try {
    return await mutate();
  } catch (err) {
    for (const [key, data] of before) qc.setQueryData(key, data);
    throw err;
  } finally {
    // Not awaited: the answer is known, and a slow re-read must not hold it.
    for (const e of edits) void qc.invalidateQueries({ queryKey: e.queryKey });
  }
}
