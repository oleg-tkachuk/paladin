// The buckets, as the console reads them: one cached list per read, under one
// key, so a change made on one page shows on every page holding a list.
//
// Every read follows nextPageToken to the end. ListBuckets pushes the
// SQL-expressible part of a filter into the query and re-evaluates the whole
// expression over the page it read, so a filter the pushdown cannot express
// returns a sparse page with a next token rather than an error: paging to the
// end is correct for every filter, and the pushdown only decides how few pages
// that takes. A read cut at one page hid every bucket past it, everywhere.
import { queryOptions } from "@tanstack/react-query";

import type { Bucket } from "@/gen/paladin/admin/v1/types_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";
import { bucketClient } from "@/lib/connect/client";

import { MAX_LIST_PAGES } from "./paging";

/** Every buckets list, whatever its read. */
export const BUCKETS_QUERY_KEY = ["buckets"] as const;

/**
 * Narrows a bucket listing to one tenant, on the server. Both fields are
 * tenant UUIDs and combine by AND; leave both unset for the cross-tenant
 * listing.
 */
export interface BucketTenantScope {
  /** Buckets the tenant owns — a dedicated-layout tenant's own bucket. */
  ownerTenantId?: string;
  /**
   * Buckets the tenant stores in: owned, bound by default, or holding one of
   * its collections.
   */
  usedByTenantId?: string;
}

/** Which buckets a page reads. */
export type BucketRead =
  /** Every bucket, or one backend's, narrowed by a CEL filter and a tenant. */
  | { backendId?: string; filter?: string; tenantScope?: BucketTenantScope }
  /**
   * The buckets a tenant may bind collections to ("tenants/<id>"): the ones it
   * owns and the ones granted it (ListAccessibleBuckets, ADR-0032). Not the
   * owner filter: a shared-layout tenant owns none and binds only granted ones.
   */
  | { accessibleTo: string };

async function pages(
  read: (pageToken: string) => Promise<{
    buckets: Bucket[];
    page?: { nextPageToken: string };
  }>,
): Promise<Bucket[]> {
  const acc: Bucket[] = [];
  let token = "";
  let count = 0;
  do {
    const res = await read(token);
    acc.push(...res.buckets);
    token = res.page?.nextPageToken ?? "";
    count += 1;
  } while (token && count < MAX_LIST_PAGES);
  return acc;
}

/** One page of the read, from `pageToken` on. */
function pageOf(read: BucketRead, pageToken: string, signal?: AbortSignal) {
  const page = { pageSize: API_PAGE_SIZE_MAX, pageToken };
  if ("accessibleTo" in read) {
    return bucketClient.listAccessibleBuckets(
      { tenant: read.accessibleTo, page },
      { signal },
    );
  }
  return bucketClient.listBuckets(
    {
      parent: read.backendId ? `storageBackends/${read.backendId}` : "",
      page,
      filter: read.filter ?? "",
      ownerTenantId: read.tenantScope?.ownerTenantId ?? "",
      usedByTenantId: read.tenantScope?.usedByTenantId ?? "",
    },
    { signal },
  );
}

/** A read's place in the cache. */
function keyOf(read: BucketRead): readonly string[] {
  if ("accessibleTo" in read) {
    return [...BUCKETS_QUERY_KEY, "accessible", read.accessibleTo];
  }
  return [
    ...BUCKETS_QUERY_KEY,
    "list",
    read.backendId ?? "",
    read.filter ?? "",
    read.tenantScope?.ownerTenantId ?? "",
    read.tenantScope?.usedByTenantId ?? "",
  ];
}

export function bucketsQuery(read: BucketRead = {}) {
  return queryOptions({
    queryKey: keyOf(read),
    queryFn: ({ signal }) =>
      pages((pageToken) => pageOf(read, pageToken, signal)),
  });
}
