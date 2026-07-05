"use client";

import { useCallback, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";

import { tenantClient } from "@/lib/connect/client";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { TenantSchema } from "@/gen/paladin/admin/v1/types_pb";
import type { StorageMigrationStatus } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { useBumpRefresh } from "@/context/RefreshContext";
import { API_PAGE_SIZE_MAX } from "@/constants";

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

export function useTenants() {
  const bumpRefresh = useBumpRefresh();
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // fetchTenants — defaults to the active set (deleted_at IS NULL).
  // Pass `includeTrashed: true` to get both; `onlyTrashed: true` to
  // get just the trash. /trash page uses onlyTrashed; default everywhere
  // else stays clean so deleted rows can't accept new bindings.
  const fetchTenants = useCallback(
    async (
      filter: string = "",
      pageToken: string = "",
      opts: { includeTrashed?: boolean; onlyTrashed?: boolean } = {},
    ) => {
      setLoading(true);
      setError(null);
      try {
        const res = await tenantClient.listTenants({
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken },
          filter,
          includeTrashed: !!opts.includeTrashed,
          onlyTrashed: !!opts.onlyTrashed,
        });
        setTenants(res.tenants);
        return {
          tenants: res.tenants,
          nextPageToken: res.page?.nextPageToken ?? "",
        };
      } catch (err) {
        // Query contract (state-only): surface via `error`, never throw.
        // Callers read `error`; awaiting callers get an empty page.
        setError(
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to fetch tenants",
        );
        return { tenants: [], nextPageToken: "" };
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  // createTenant — Phase 0 + default-binding contract:
  //   - slug is required (caller-supplied kebab-case handle).
  //   - tenantId is optional; empty string lets the server mint UUIDv7.
  //   - displayName is optional; defaults to slug when empty.
  //   - defaultBucket — resource name "storageBackends/{b}/buckets/{bk}";
  //     when non-empty, the server pins this tenant's default
  //     (backend, bucket) in tenant_default_bindings. Optional; legacy
  //     bootstrap callers omit it. For a dedicated tenant, send a
  //     backend-only ref "storageBackends/{b}/buckets/" (bucket derived).
  //   - storageLayout — "shared" (default) or "dedicated" (ADR-0011): a
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
        setError(null);
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
        setTenants((prev) => [...prev, created]);
        bumpRefresh("tenants");
        return created;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [],
  );

  const updateTenantMetadata = useCallback(
    async (
      tenantId: string,
      resourceVersion: string,
      displayName: string,
      labels: Record<string, string> = {},
    ): Promise<Tenant> => {
      try {
        setError(null);
        const tenant = create(TenantSchema, {
          name: tenantResourceName(tenantId),
          tenantId,
          displayName,
          labels,
          inheritedCedarPolicy: "",
          resourceVersion,
        });
        const updated = await tenantClient.updateTenant({
          name: tenantResourceName(tenantId),
          resourceVersion,
          updateMask: create(FieldMaskSchema, {
            paths: ["display_name", "labels"],
          }),
          tenant,
        });
        setTenants((prev) =>
          prev.map((t) => (t.tenantId === tenantId ? updated : t)),
        );
        bumpRefresh("tenants");
        return updated;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [],
  );

  // deleteTenant — defaults to SOFT delete (server moves the row to
  // trash with deleted_at set). Pass `force=true` to skip the trash
  // and hard-delete in one shot (E2E cleanups, emergency purge from
  // an active tenant).
  const deleteTenant = useCallback(
    async (
      tenantId: string,
      resourceVersion: string = "",
      force: boolean = false,
    ): Promise<void> => {
      try {
        setError(null);
        await tenantClient.deleteTenant({
          name: tenantResourceName(tenantId),
          resourceVersion,
          force,
        });
        setTenants((prev) => prev.filter((t) => t.tenantId !== tenantId));
        bumpRefresh("tenants");
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [],
  );

  // restoreTenant — clears deleted_at on a trashed row. Returns the
  // restored Tenant (with deleted_at unset). Slug/display_name UNIQUE
  // collisions surface as ALREADY_EXISTS — operator must rename the
  // active claimer first.
  const restoreTenant = useCallback(
    async (tenantId: string): Promise<Tenant> => {
      try {
        setError(null);
        const restored = await tenantClient.restoreTenant({
          name: tenantResourceName(tenantId),
        });
        setTenants((prev) =>
          prev.map((t) => (t.tenantId === tenantId ? restored : t)),
        );
        bumpRefresh("tenants");
        return restored;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [],
  );

  // purgeTenant — hard-deletes a soft-deleted row. The server refuses
  // to operate on an active tenant (FAILED_PRECONDITION) so callers
  // must soft-delete first.
  const purgeTenant = useCallback(async (tenantId: string): Promise<void> => {
    try {
      setError(null);
      await tenantClient.purgeTenant({
        name: tenantResourceName(tenantId),
      });
      setTenants((prev) => prev.filter((t) => t.tenantId !== tenantId));
      bumpRefresh("tenants");
    } catch (err) {
      // Mutation contract (throw-only): caller surfaces via errorMessage().
      throw err;
    }
  }, []);

  // getTenantStorageMigration returns the tenant's shared->dedicated migration
  // status, or null when none was ever started (NOT_FOUND). See ADR-0011 Phase 3.
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

  // migrateTenantStorageLayout starts a shared->dedicated migration (ADR-0011
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
    loading,
    error,
    fetchTenants,
    createTenant,
    updateTenantMetadata,
    deleteTenant,
    restoreTenant,
    purgeTenant,
    getTenantStorageMigration,
    migrateTenantStorageLayout,
  };
}
