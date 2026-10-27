// The storage backends, as the console reads them: one cached list per
// filter, under one key, so a change made on one page shows on every page
// holding a list.
import { queryOptions } from "@tanstack/react-query";

import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";
import { MAX_LIST_PAGES } from "@/hooks/useBuckets";
import { backendClient } from "@/lib/connect/client";

/** A backend's resource name, as BackendService and its policy layer take it. */
export const backendResourceName = (backendId: string) =>
  `storageBackends/${backendId}`;

/** Every backends list, whatever its filter. */
export const BACKENDS_QUERY_KEY = ["backends"] as const;

/**
 * Every backend `filter` (CEL, "" for none) admits, every page of it.
 * ListBackends re-evaluates the whole filter over the page it read, so an
 * expression the pushdown cannot express returns a sparse page with a next
 * token rather than an error: paging to the end is correct for every filter.
 */
async function listBackends(
  filter: string,
  signal?: AbortSignal,
): Promise<StorageBackend[]> {
  const acc: StorageBackend[] = [];
  let token = "";
  let pages = 0;
  do {
    const res = await backendClient.listBackends(
      { page: { pageSize: API_PAGE_SIZE_MAX, pageToken: token }, filter },
      { signal },
    );
    acc.push(...res.backends);
    token = res.page?.nextPageToken ?? "";
    pages += 1;
  } while (token && pages < MAX_LIST_PAGES);
  return acc;
}

export function backendsQuery(filter: string = "") {
  return queryOptions({
    queryKey: [...BACKENDS_QUERY_KEY, filter] as const,
    queryFn: ({ signal }) => listBackends(filter, signal),
  });
}
