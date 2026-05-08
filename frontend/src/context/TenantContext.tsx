"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { ConnectError } from "@connectrpc/connect";

import { tenantClient } from "@/lib/connect/client";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { useAuth } from "@/context/AuthContext";

/**
 * TenantContext — derives the active tenant from the signed-in user's JWT
 * (one-user-one-tenant model agreed in the integration plan).
 *
 * Source of truth:
 *   - tenantId  : useAuth().user.tenantId — comes from the JWT `tenant` claim
 *                 via WhoAmI, never from localStorage
 *   - tenant    : full record fetched once via TenantService.GetTenant
 *
 * The legacy `tenants[]` / `setTenantId` / `refreshTenants` API is kept on
 * the context shape (as no-op / single-element) so existing consumers
 * (TenantSwitcher, /tenants page) compile while the multi-tenant switcher
 * UX is being retired. New code should read only `tenantId` and `tenant`.
 */

interface TenantContextType {
  tenantId: string | null;
  tenant: Tenant | null;
  /** @deprecated 1:1 model: always [tenant] when loaded, [] otherwise. */
  tenants: Tenant[];
  /** @deprecated tenant comes from JWT; switching requires a different login. */
  setTenantId: (id: string) => void;
  /** @deprecated kept for back-compat; refetches the single tenant record. */
  refreshTenants: () => Promise<void>;
  isLoading: boolean;
}

const TenantContext = createContext<TenantContextType | undefined>(undefined);

export function TenantProvider({ children }: { children: ReactNode }) {
  const { user, status } = useAuth();
  const tenantId = user?.tenantId ?? null;

  const [tenant, setTenant] = useState<Tenant | null>(null);
  const [isLoading, setIsLoading] = useState(false);

  const loadTenant = useCallback(async () => {
    if (!tenantId) {
      setTenant(null);
      return;
    }
    setIsLoading(true);
    try {
      const fetched = await tenantClient.getTenant({
        name: `tenants/${tenantId}`,
      });
      setTenant(fetched ?? null);
    } catch (err) {
      // 404 / NotFound is plausible right after signup or in dev where the
      // JWT carries a tenant id that doesn't exist yet — log and move on
      // rather than red-toasting.
      if (err instanceof ConnectError) {
        console.warn(
          `[TenantContext] getTenant(${tenantId}) failed:`,
          err.rawMessage,
        );
      } else {
        console.error("[TenantContext] getTenant failed:", err);
      }
      setTenant(null);
    } finally {
      setIsLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    if (status !== "authenticated") {
      setTenant(null);
      return;
    }
    void loadTenant();
  }, [status, loadTenant]);

  const setTenantId = useCallback((_id: string) => {
    if (process.env.NODE_ENV !== "production") {
      console.warn(
        "[TenantContext] setTenantId() is a no-op in 1:1 model — tenant comes from JWT.",
      );
    }
  }, []);

  const refreshTenants = useCallback(async () => {
    await loadTenant();
  }, [loadTenant]);

  const tenants = useMemo(() => (tenant ? [tenant] : []), [tenant]);

  const value = useMemo<TenantContextType>(
    () => ({
      tenantId,
      tenant,
      tenants,
      setTenantId,
      refreshTenants,
      isLoading,
    }),
    [tenantId, tenant, tenants, setTenantId, refreshTenants, isLoading],
  );

  return (
    <TenantContext.Provider value={value}>{children}</TenantContext.Provider>
  );
}

export function useTenant() {
  const ctx = useContext(TenantContext);
  if (!ctx) {
    throw new Error("useTenant must be used within a TenantProvider");
  }
  return ctx;
}
