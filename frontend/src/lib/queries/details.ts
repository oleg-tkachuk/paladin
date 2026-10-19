// What a detail page reads, as query options both the page and a list's row
// use: the row warms the cache on hover or focus with exactly the query the
// page will then answer from it.
import { queryOptions } from "@tanstack/react-query";

import { collectionClient, userClient } from "@/lib/connect/client";

/** A collection's resource name, as GetCollection takes it. */
export const collectionResourceName = (tenantId: string, collection: string) =>
  `tenants/${tenantId}/collections/${collection}`;

export function collectionDetailQuery(tenantId: string, collection: string) {
  return queryOptions({
    queryKey: ["collection", tenantId, collection] as const,
    retry: false, // NotFound short-circuits to notFound(); a retry won't help.
    queryFn: ({ signal }) =>
      collectionClient.getCollection(
        { name: collectionResourceName(tenantId, collection) },
        { signal },
      ),
  });
}

export function userDetailQuery(name: string) {
  return queryOptions({
    queryKey: ["user", name] as const,
    queryFn: ({ signal }) => userClient.getUser({ name }, { signal }),
  });
}

/** Every user, as the users page lists them. */
export const USERS_LIST_QUERY_KEY = ["users"] as const;
