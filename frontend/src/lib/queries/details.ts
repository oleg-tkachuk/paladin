// What a detail page reads, as query options both the page and a list's row
// use: the row warms the cache on hover or focus with exactly the query the
// page will then answer from it.
import { queryOptions } from "@tanstack/react-query";
import { Code, ConnectError } from "@connectrpc/connect";

import {
  bucketClient,
  capabilityClient,
  collectionClient,
  policyClient,
  quotaClient,
  tenantClient,
  userClient,
} from "@/lib/connect/client";
import { notFoundIsAnswer } from "@/lib/connect/expected";
import type { Money } from "@/gen/google/type/money_pb";
import type { CapabilityLeaseEntry } from "@/lib/capabilityLease";

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

/**
 * Every capability record's query key starts so: a revoke changes the
 * revoked capability's record and its descendants', which are stopped
 * through it.
 */
export const CAPABILITY_RECORDS_KEY = ["capability"] as const;

export function capabilityDetailQuery(id: string) {
  return queryOptions({
    queryKey: [...CAPABILITY_RECORDS_KEY, id] as const,
    retry: false, // NotFound: no capability has the id, or it is not visible.
    queryFn: ({ signal }) => capabilityClient.get({ id }, { signal }),
  });
}

/**
 * What a capability has used: its requests and its spend, or "never" when it
 * has no usage row yet — GetUsage's NotFound is that answer, not a failure.
 */
export type CapabilityUsage =
  { requestCount: bigint; spent: Money | undefined } | "never";

/** A capability's usage, read once for the list's rows and the panel alike. */
export async function fetchCapabilityUsage(
  id: string,
  signal?: AbortSignal,
): Promise<CapabilityUsage> {
  try {
    const u = await capabilityClient.getUsage(
      { id },
      notFoundIsAnswer({ signal }),
    );
    return { requestCount: u.requestCount, spent: u.spent };
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.NotFound) {
      return "never";
    }
    throw err;
  }
}

/** A capability's budget lease, or "none" when it holds none. */
export async function fetchCapabilityLease(
  id: string,
  signal?: AbortSignal,
): Promise<CapabilityLeaseEntry> {
  try {
    const res = await capabilityClient.getLease(
      { id },
      notFoundIsAnswer({ signal }),
    );
    return res.lease ?? "none";
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.NotFound) {
      return "none";
    }
    throw err;
  }
}

export function capabilityLeaseQuery(id: string) {
  return queryOptions({
    queryKey: ["capabilityLease", id] as const,
    retry: false,
    queryFn: ({ signal }) => fetchCapabilityLease(id, signal),
  });
}

export function capabilityUsageQuery(id: string) {
  return queryOptions({
    queryKey: ["capabilityUsage", id] as const,
    retry: false,
    queryFn: ({ signal }) => fetchCapabilityUsage(id, signal),
  });
}

/** Every user, as the users page lists them. */
export const USERS_LIST_QUERY_KEY = ["users"] as const;

/** A tenant by its id or its slug, as GetTenant takes either. */
export function tenantDetailQuery(handle: string) {
  return queryOptions({
    queryKey: ["tenantDetail", handle] as const,
    retry: false, // NotFound: no tenant has the handle; a retry won't help.
    queryFn: ({ signal }) =>
      tenantClient.getTenant({ name: `tenants/${handle}` }, { signal }),
  });
}

export function bucketDetailQuery(backendId: string, bucketId: string) {
  return queryOptions({
    queryKey: ["bucketDetail", backendId, bucketId] as const,
    retry: false, // NotFound short-circuits to notFound(); a retry won't help.
    queryFn: ({ signal }) =>
      bucketClient.getBucket(
        { name: `storageBackends/${backendId}/buckets/${bucketId}` },
        { signal },
      ),
  });
}

/** A tenant's quota, as GetQuota names it. */
export const tenantQuotaName = (tenantId: string) =>
  `tenants/${tenantId}/quota`;

/**
 * A tenant's quota, or none: a tenant that never had one set has no row, and
 * NotFound is that answer, not a failure. Shared by the quotas tab and the
 * overview's status panel.
 */
export async function fetchTenantQuota(name: string, signal?: AbortSignal) {
  try {
    const quota = await quotaClient.getQuota(
      { name },
      notFoundIsAnswer({ signal }),
    );
    return { quota, notFound: false };
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.NotFound) {
      return { quota: null, notFound: true };
    }
    throw err;
  }
}

export function tenantQuotaQuery(tenantId: string) {
  const name = tenantQuotaName(tenantId);
  return queryOptions({
    queryKey: ["tenantQuota", name] as const,
    retry: false,
    queryFn: ({ signal }) => fetchTenantQuota(name, signal),
  });
}

/** The policy layers the authorizer evaluates for a resource, in order. */
export function effectivePolicyQuery(resourceName: string) {
  return queryOptions({
    queryKey: ["effective-policy", resourceName] as const,
    queryFn: ({ signal }) =>
      policyClient.getEffectivePolicy({ resourceName }, { signal }),
  });
}
