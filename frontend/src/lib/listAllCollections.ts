import type { Collection } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";
import { collectionClient } from "@/lib/connect/client";
import { MAX_LIST_PAGES } from "@/hooks/useBuckets";

/** What a list of Collections is narrowed by; see ListCollectionsRequest. */
export interface CollectionScope {
  /** "tenants/{id}", or "" across tenants (platform admin). */
  parent: string;
  /** A bucket's resource name, to list only what is bound to it. */
  bucket?: string;
  filter?: string;
}

/**
 * Every Collection in scope, following next_page_token to the end. A tab
 * that shows what a tenant or a bucket holds and reads one page claims the
 * rest are not there.
 */
export async function listAllCollections(
  scope: CollectionScope,
  signal?: AbortSignal,
): Promise<Collection[]> {
  const acc: Collection[] = [];
  let token = "";
  let pages = 0;
  do {
    const res = await collectionClient.listCollections(
      {
        parent: scope.parent,
        bucket: scope.bucket ?? "",
        filter: scope.filter ?? "",
        page: { pageSize: API_PAGE_SIZE_MAX, pageToken: token },
      },
      { signal },
    );
    acc.push(...res.collections);
    token = res.page?.nextPageToken ?? "";
    pages += 1;
  } while (token && pages < MAX_LIST_PAGES);
  return acc;
}
