"use client";

import { useCallback, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { FieldMaskSchema } from "@bufbuild/protobuf/wkt";
import { ConnectError } from "@connectrpc/connect";

import { tenantClient } from "@/lib/connect/client";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { TenantSchema } from "@/gen/paladin/admin/v1/types_pb";
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

  const fetchTenants = useCallback(
    async (filter: string = "", pageToken: string = "") => {
      setLoading(true);
      setError(null);
      try {
        const res = await tenantClient.listTenants({
          page: { pageSize: API_PAGE_SIZE_MAX, pageToken },
          filter,
        });
        setTenants(res.tenants);
        return {
          tenants: res.tenants,
          nextPageToken: res.page?.nextPageToken ?? "",
        };
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to fetch tenants";
        setError(msg);
        throw err;
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  // createTenant — Phase 0 contract:
  //   - slug is required (caller-supplied kebab-case handle).
  //   - tenantId is optional; empty string lets the server mint UUIDv7.
  //   - displayName is optional; the server defaults it to slug when empty.
  // The previous signature (id, name) has been replaced — id-only callers
  // need to choose a slug before they can land a tenant.
  const createTenant = useCallback(
    async (
      slug: string,
      tenantId: string = "",
      displayName: string = "",
      labels: Record<string, string> = {},
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
          inheritedCedarPolicy: "",
          resourceVersion: "",
        });
        const created = await tenantClient.createTenant({ tenantId, tenant });
        setTenants((prev) => [...prev, created]);
        bumpRefresh("tenants");
        return created;
      } catch (err) {
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to create tenant";
        setError(msg);
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
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to update tenant";
        setError(msg);
        throw err;
      }
    },
    [],
  );

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
        const msg =
          err instanceof ConnectError
            ? err.rawMessage
            : "Failed to delete tenant";
        setError(msg);
        throw err;
      }
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
  };
}
