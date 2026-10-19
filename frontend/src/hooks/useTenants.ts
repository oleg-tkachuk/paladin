"use client";

import { useCallback, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";

import { tenantClient } from "@/lib/connect/client";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { TenantSchema } from "@/gen/paladin/admin/v1/types_pb";
import type { StorageMigrationStatus } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { useBumpRefresh } from "@/context/RefreshContext";
import { errorMessage } from "@/hooks/errorContract";
import { fieldMask } from "@/lib/connect/fieldMask";
import { useOptimistic } from "@/hooks/useOptimistic";
import { edit, type OptimisticEdit } from "@/lib/optimistic";
import {
  TENANTS_QUERY_KEY,
  tenantsQuery,
  type TenantSet,
} from "@/lib/queries/tenants";

/**
 * useTenants — wrapper around admin/v1.TenantService.
 *
 * The new request shapes nest pagination under `page` (PageRequest) and
 * carry the resource as a `tenant` sub-message instead of flat fields.
 * This hook keeps the legacy call signatures the /tenants page already
 * uses (`createTenant(id, name)`, `updateTenantMetadata(id, rv, name,
 * labels)`, `deleteTenant(id)`) so the page didn't need to change beyond
 * its hook import.
 */

const tenantResourceName = (id: string) => `tenants/${id}`;

/** A tenant's fields as an edit leaves them, in every list that holds it. */
function changedIn(tenantId: string, fields: Partial<Tenant>) {
  return (list: Tenant[]) =>
    list.map((t) => (t.tenantId === tenantId ? { ...t, ...fields } : t));
}

/** A tenant taken out of the lists of one set. */
function removedFrom(set: TenantSet, tenantId: string): OptimisticEdit {
  return edit<Tenant[]>(tenantsQuery(set).queryKey, (list) =>
    list.filter((t) => t.tenantId !== tenantId),
  );
}

/**
 * The tenants of a set, read through the query cache (lib/queries/tenants),
 * and the changes to them. A change the console can predict — a rename, a
 * trash, a restore, a purge — shows in every cached list at once and is put
 * back if the server refuses it (lib/optimistic).
 */
export function useTenants(set: TenantSet = "active") {
  const bumpRefresh = useBumpRefresh();
  const qc = useQueryClient();
  const optimistic = useOptimistic();
  const query = useQuery(tenantsQuery(set));
  const tenants = useMemo(() => query.data ?? [], [query.data]);
  const error = query.error
    ? errorMessage(query.error, "Failed to fetch tenants")
    : null;
  const { refetch: refetchQuery } = query;
  const refetch = useCallback(() => void refetchQuery(), [refetchQuery]);

  // What the server answered differs from any prediction in other sets (a
  // trashed tenant joins the trash), so every list reads again after.
  const settle = useCallback(() => {
    void qc.invalidateQueries({ queryKey: TENANTS_QUERY_KEY });
    bumpRefresh("tenants");
  }, [qc, bumpRefresh]);

  // createTenant — Phase 0 + default-binding contract:
  //   - slug is required (caller-supplied kebab-case handle).
  //   - tenantId is optional; empty string lets the server mint UUIDv7.
  //   - displayName is optional; defaults to slug when empty.
  //   - defaultBucket — resource name "storageBackends/{b}/buckets/{bk}";
  //     when non-empty, the server pins this tenant's default
  //     (backend, bucket) in tenant_default_bindings. Optional; legacy
  //     bootstrap callers omit it. For a dedicated tenant, send a
  //     backend-only ref "storageBackends/{b}/buckets/" (bucket derived).
  //   - storageLayout — "shared" (default) or "dedicated" (ADR-0015): a
  //     dedicated tenant gets its own provisioned bucket on the named backend;
  //     the backend is required, the bucket is derived (paladin-<tenant_uuid>).
  const createTenant = useCallback(
    async (
      slug: string,
      tenantId: string = "",
      displayName: string = "",
      labels: Record<string, string> = {},
      defaultBucket: string = "",
      storageLayout: string = "shared",
    ): Promise<Tenant> => {
      try {
        const tenant = create(TenantSchema, {
          // name is server-derived — caller's value is ignored at the
          // server but we set it to "" to keep the wire shape clean.
          name: "",
          tenantId,
          slug,
          displayName,
          labels,
          storageLayout,
          inheritedCedarPolicy: "",
          resourceVersion: "",
        });
        const created = await tenantClient.createTenant({
          tenantId,
          tenant,
          defaultBucket,
        });
        // The server mints the id and the name: nothing to predict.
        settle();
        return created;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [settle],
  );

  const updateTenantMetadata = useCallback(
    async (
      tenantId: string,
      resourceVersion: string,
      displayName: string,
      labels: Record<string, string> = {},
    ): Promise<Tenant> => {
      const tenant = create(TenantSchema, {
        name: tenantResourceName(tenantId),
        tenantId,
        displayName,
        labels,
        inheritedCedarPolicy: "",
        resourceVersion,
      });
      try {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        return await optimistic(
          [
            edit<Tenant[]>(
              TENANTS_QUERY_KEY,
              changedIn(tenantId, { displayName, labels }),
            ),
          ],
          () =>
            tenantClient.updateTenant({
              name: tenantResourceName(tenantId),
              resourceVersion,
              updateMask: fieldMask(TenantSchema, "displayName", "labels"),
              tenant,
            }),
        );
      } finally {
        settle();
      }
    },
    [optimistic, settle],
  );

  // deleteTenant — moves the tenant to the trash (the server sets deleted_at)
  // and does nothing else. The `force` argument that used to hard-delete in
  // one shot is gone: landing the tenant in a different state is a different
  // transition, and purgeTenant is that transition.
  const deleteTenant = useCallback(
    async (tenantId: string, resourceVersion: string = ""): Promise<void> => {
      try {
        await optimistic([removedFrom("active", tenantId)], () =>
          tenantClient.deleteTenant({
            name: tenantResourceName(tenantId),
            resourceVersion,
          }),
        );
      } finally {
        settle();
      }
    },
    [optimistic, settle],
  );

  // restoreTenant — clears deleted_at on a trashed row. Returns the
  // restored Tenant (with deleted_at unset). Slug/display_name UNIQUE
  // collisions surface as ALREADY_EXISTS — operator must rename the
  // active claimer first.
  const restoreTenant = useCallback(
    async (tenantId: string): Promise<Tenant> => {
      try {
        return await optimistic([removedFrom("trashed", tenantId)], () =>
          tenantClient.restoreTenant({ name: tenantResourceName(tenantId) }),
        );
      } finally {
        settle();
      }
    },
    [optimistic, settle],
  );

  // purgeTenant — hard-deletes a soft-deleted row. The server refuses
  // to operate on an active tenant (FAILED_PRECONDITION) so callers
  // must soft-delete first.
  const purgeTenant = useCallback(
    async (tenantId: string): Promise<void> => {
      try {
        await optimistic(
          [removedFrom("trashed", tenantId), removedFrom("all", tenantId)],
          () =>
            tenantClient.purgeTenant({ name: tenantResourceName(tenantId) }),
        );
      } finally {
        settle();
      }
    },
    [optimistic, settle],
  );

  // getTenantStorageMigration returns the tenant's shared->dedicated migration
  // status, or null when none was ever started (NOT_FOUND). See ADR-0015 Phase 3.
  const getTenantStorageMigration = useCallback(
    async (tenantId: string): Promise<StorageMigrationStatus | null> => {
      try {
        return await tenantClient.getTenantStorageMigration({
          name: `tenants/${tenantId}`,
        });
      } catch (err) {
        if (err instanceof ConnectError && err.code === Code.NotFound) {
          return null;
        }
        throw err;
      }
    },
    [],
  );

  // migrateTenantStorageLayout starts a shared->dedicated migration (ADR-0015
  // Phase 3). The tenant MUST currently be shared. targetBackendId empty reuses
  // the current backend; cleanupRetentionSeconds 0 uses the server default (24h).
  // Returns the freshly-created migration status.
  const migrateTenantStorageLayout = useCallback(
    async (
      tenantId: string,
      opts?: { targetBackendId?: string; cleanupRetentionSeconds?: number },
    ): Promise<StorageMigrationStatus> => {
      return await tenantClient.migrateTenantStorageLayout({
        name: `tenants/${tenantId}`,
        targetBackendId: opts?.targetBackendId ?? "",
        cleanupRetentionSeconds: BigInt(opts?.cleanupRetentionSeconds ?? 0),
      });
    },
    [],
  );

  return {
    tenants,
    loading: query.isFetching,
    error,
    refetch,
    /** When the list was last read, for ages measured against it. */
    fetchedAt: query.dataUpdatedAt,
    createTenant,
    updateTenantMetadata,
    deleteTenant,
    restoreTenant,
    purgeTenant,
    getTenantStorageMigration,
    migrateTenantStorageLayout,
  };
}
