// The collections, as the console reads them: one cached list per scope and
// search, under one key, so a change made on one page shows on every page
// holding a list.
import { queryOptions } from "@tanstack/react-query";

import { listAllCollections } from "@/lib/listAllCollections";

/** Every collections list, whatever its scope or search. */
export const COLLECTIONS_QUERY_KEY = ["collections"] as const;

/** Where the scope sits in a list's key, to tell one tenant's from another's. */
export const COLLECTIONS_PARENT_INDEX = COLLECTIONS_QUERY_KEY.length;

/**
 * Every collection under `parent` ("tenants/<id>", or "" for the platform's
 * catalogue) that `filter` (CEL, "" for none) admits, every page of it.
 */
export function collectionsQuery(parent: string, filter: string = "") {
  return queryOptions({
    queryKey: [...COLLECTIONS_QUERY_KEY, parent, filter] as const,
    queryFn: ({ signal }) => listAllCollections({ parent, filter }, signal),
  });
}
