// The tenants, as the console reads them: one cached list per set, under one
// key, so a change made on one page shows on every page holding a list.
import { queryOptions } from "@tanstack/react-query";

import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import type { RetiredTenantSlug } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { API_PAGE_SIZE_MAX } from "@/constants";
import { MAX_LIST_PAGES } from "./paging";
import { tenantClient } from "@/lib/connect/client";

/**
 * Which tenants: the live ones (the default everywhere a tenant is picked or
 * named), the trash, or both (billing, whose ledger keeps a trashed tenant's
 * charges).
 */
export type TenantSet = "active" | "trashed" | "all";

/** Every tenants list, whatever its set. */
export const TENANTS_QUERY_KEY = ["tenants"] as const;

/** The flags ListTenants takes for a set. */
const SET_FLAGS: Record<
  TenantSet,
  { includeTrashed: boolean; onlyTrashed: boolean }
> = {
  active: { includeTrashed: false, onlyTrashed: false },
  trashed: { includeTrashed: false, onlyTrashed: true },
  all: { includeTrashed: true, onlyTrashed: false },
};

/**
 * Every page of a set, up to the ceiling: the callers name owners and
 * grantees and pick a tenant from it, and a set cut at one page hid every
 * tenant past it.
 */
async function listTenants(
  set: TenantSet,
  signal?: AbortSignal,
): Promise<Tenant[]> {
  const acc: Tenant[] = [];
  let token = "";
  let pages = 0;
  do {
    const res = await tenantClient.listTenants(
      {
        page: { pageSize: API_PAGE_SIZE_MAX, pageToken: token },
        filter: "",
        ...SET_FLAGS[set],
      },
      { signal },
    );
    acc.push(...res.tenants);
    token = res.page?.nextPageToken ?? "";
    pages += 1;
  } while (token && pages < MAX_LIST_PAGES);
  return acc;
}

export function tenantsQuery(set: TenantSet = "active") {
  return queryOptions({
    queryKey: [...TENANTS_QUERY_KEY, set] as const,
    queryFn: ({ signal }) => listTenants(set, signal),
  });
}

/** The segment the purge previews of tenants sit under, beside the sets. */
const PURGE_PREVIEW_SEGMENT = "purgePreview";

/**
 * What a purge of the tenant would remove and what would refuse it, read
 * each time the confirmation opens: under the tenants' key, so a change to
 * any tenant drops it, and never served stale.
 */
export function purgePreviewQuery(tenantId: string) {
  return queryOptions({
    queryKey: [...TENANTS_QUERY_KEY, PURGE_PREVIEW_SEGMENT, tenantId] as const,
    queryFn: ({ signal }) =>
      tenantClient.previewPurgeTenant(
        { name: `tenants/${tenantId}` },
        { signal },
      ),
    staleTime: 0,
  });
}

/** The segment the retired slugs sit under, beside the sets. */
const RETIRED_SLUGS_SEGMENT = "retiredSlugs";

/** The slugs purged tenants held, and whether a new tenant is refused them. */
export interface RetiredSlugs {
  slugs: RetiredTenantSlug[];
  /** admin.tenants.reserve_slugs: false records them and refuses nothing. */
  enforced: boolean;
}

async function listRetiredSlugs(signal?: AbortSignal): Promise<RetiredSlugs> {
  const slugs: RetiredTenantSlug[] = [];
  let enforced = true;
  let token = "";
  let pages = 0;
  do {
    const res = await tenantClient.listRetiredTenantSlugs(
      { page: { pageSize: API_PAGE_SIZE_MAX, pageToken: token } },
      { signal },
    );
    slugs.push(...res.retiredSlugs);
    enforced = res.reservationEnforced;
    token = res.page?.nextPageToken ?? "";
    pages += 1;
  } while (token && pages < MAX_LIST_PAGES);
  return { slugs, enforced };
}

/**
 * The retired slugs, under the tenants' key: a purge adds one, so the
 * invalidation every tenant change makes reads them again.
 */
export function retiredSlugsQuery() {
  return queryOptions({
    queryKey: [...TENANTS_QUERY_KEY, RETIRED_SLUGS_SEGMENT] as const,
    queryFn: ({ signal }) => listRetiredSlugs(signal),
  });
}
