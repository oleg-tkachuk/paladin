"use client";

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { ConnectError } from "@connectrpc/connect";

import { DEFAULT_OBJECT_KEY, STORAGE_KEYS } from "@/constants";
import { tenantClient } from "@/lib/connect/client";
import type { Tenant } from "@/gen/paladin/admin/v1/types_pb";
import { useAuth } from "@/context/AuthContext";

/**
 * ScopeContext — single source of truth for the operator's location
 * inside the control plane. Merges what used to live in TenantContext
 * + ScopeContext so consumers only have to learn one hook.
 *
 * Hard scope (derived from the JWT, not user-selectable):
 *   tenantId  → useAuth().user.tenantId — comes from the JWT `tenant`
 *               claim via WhoAmI, never from localStorage. The 1:1
 *               user-tenant model means switching tenants requires a
 *               different login. ListMyMemberships + SwitchTenant on
 *               the backend would relax that; until then the tenant
 *               row in <ScopePicker> is read-only.
 *   tenant    → full record fetched once via TenantService.GetTenant.
 *
 * Soft scope (user-selectable, persisted to localStorage):
 *   backendId → which storage backend the user is focused on
 *   bucketName→ which physical bucket the user is focused on (cleared
 *               when backendId changes — a bucket only makes sense in
 *               the context of one backend)
 *   objectKey → which ObjectKey namespace the user is browsing.
 *               Single source of truth so /objects, sidebar counts,
 *               CommandPalette searches, and ObjectInspector always
 *               agree.
 *
 * UI bus:
 *   isPickerOpen / openScopePicker / closeScopePicker — lets
 *   PageHeader breadcrumb segments and other surfaces trigger the
 *   shared <ScopePicker> popover without prop-drilling a ref.
 *
 * Pages that don't `useScope()` keep working unchanged.
 */
interface ScopeContextType {
  // Hard scope (JWT-derived)
  tenantId: string | null;
  tenant: Tenant | null;
  isTenantLoading: boolean;
  refreshTenant: () => Promise<void>;

  // Soft scope (localStorage-persisted)
  backendId: string | null;
  bucketName: string | null;
  objectKey: string;
  setBackend: (id: string | null) => void;
  setBucket: (name: string | null) => void;
  setObjectKey: (key: string) => void;
  // setScope writes the backend + bucket pair atomically. Picking a
  // bucket from the picker needs this — `setBackend` clears the
  // bucket as a safety net, so calling setBackend(b.backendId) then
  // setBucket(b.bucketName) leaves you with only the bucket in
  // localStorage. Use setScope when you already know the canonical
  // (backend, bucket) pair.
  setScope: (backendId: string | null, bucketName: string | null) => void;

  // UI bus — shared <ScopePicker> open state
  isPickerOpen: boolean;
  openScopePicker: () => void;
  closeScopePicker: () => void;
  setPickerOpen: (open: boolean) => void;
}

const ScopeContext = createContext<ScopeContextType | undefined>(undefined);

const safeRead = (key: string): string | null => {
  if (typeof window === "undefined") return null;
  return localStorage.getItem(key);
};

export function ScopeProvider({ children }: { children: React.ReactNode }) {
  // ── Hard scope: tenant from JWT ────────────────────────────────────────
  const { user, status } = useAuth();
  const tenantId = user?.tenantId ?? null;

  const [tenant, setTenant] = useState<Tenant | null>(null);
  const [isTenantLoading, setIsTenantLoading] = useState(false);

  const loadTenant = useCallback(async () => {
    if (!tenantId) {
      setTenant(null);
      return;
    }
    setIsTenantLoading(true);
    try {
      const fetched = await tenantClient.getTenant({
        name: `tenants/${tenantId}`,
      });
      setTenant(fetched ?? null);
    } catch (err) {
      // 404 / NotFound is plausible right after signup or in dev where
      // the JWT carries a tenant id that doesn't exist yet — log and
      // move on rather than red-toasting.
      if (err instanceof ConnectError) {
        console.warn(
          `[ScopeContext] getTenant(${tenantId}) failed:`,
          err.rawMessage,
        );
      } else {
        console.error("[ScopeContext] getTenant failed:", err);
      }
      setTenant(null);
    } finally {
      setIsTenantLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    if (status !== "authenticated") {
      setTenant(null);
      return;
    }
    void loadTenant();
  }, [status, loadTenant]);

  const refreshTenant = useCallback(async () => {
    await loadTenant();
  }, [loadTenant]);

  // ── Soft scope: backend / bucket / objectKey ───────────────────────────
  const [backendId, setBackendIdState] = useState<string | null>(() =>
    safeRead(STORAGE_KEYS.scopeBackend),
  );
  const [bucketName, setBucketNameState] = useState<string | null>(() =>
    safeRead(STORAGE_KEYS.scopeBucket),
  );
  const [objectKey, setObjectKeyState] = useState<string>(
    () => safeRead(STORAGE_KEYS.scopeObjectKey) || DEFAULT_OBJECT_KEY,
  );

  const setBackend = useCallback((id: string | null) => {
    setBackendIdState(id);
    if (id) localStorage.setItem(STORAGE_KEYS.scopeBackend, id);
    else localStorage.removeItem(STORAGE_KEYS.scopeBackend);
    // Changing the backend implicitly invalidates the bucket selection
    // (a bucket only makes sense in the context of one backend).
    setBucketNameState(null);
    localStorage.removeItem(STORAGE_KEYS.scopeBucket);
  }, []);

  const setBucket = useCallback((name: string | null) => {
    setBucketNameState(name);
    if (name) localStorage.setItem(STORAGE_KEYS.scopeBucket, name);
    else localStorage.removeItem(STORAGE_KEYS.scopeBucket);
  }, []);

  const setScope = useCallback((id: string | null, name: string | null) => {
    setBackendIdState(id);
    if (id) localStorage.setItem(STORAGE_KEYS.scopeBackend, id);
    else localStorage.removeItem(STORAGE_KEYS.scopeBackend);
    setBucketNameState(name);
    if (name) localStorage.setItem(STORAGE_KEYS.scopeBucket, name);
    else localStorage.removeItem(STORAGE_KEYS.scopeBucket);
  }, []);

  const setObjectKey = useCallback((key: string) => {
    const normalized = key?.trim() || DEFAULT_OBJECT_KEY;
    setObjectKeyState(normalized);
    localStorage.setItem(STORAGE_KEYS.scopeObjectKey, normalized);
  }, []);

  // ── UI bus ─────────────────────────────────────────────────────────────
  const [isPickerOpen, setIsPickerOpen] = useState(false);
  const openScopePicker = useCallback(() => setIsPickerOpen(true), []);
  const closeScopePicker = useCallback(() => setIsPickerOpen(false), []);
  const setPickerOpen = useCallback(
    (open: boolean) => setIsPickerOpen(open),
    [],
  );

  const value = useMemo<ScopeContextType>(
    () => ({
      tenantId,
      tenant,
      isTenantLoading,
      refreshTenant,
      backendId,
      bucketName,
      objectKey,
      setBackend,
      setBucket,
      setObjectKey,
      setScope,
      isPickerOpen,
      openScopePicker,
      closeScopePicker,
      setPickerOpen,
    }),
    [
      tenantId,
      tenant,
      isTenantLoading,
      refreshTenant,
      backendId,
      bucketName,
      objectKey,
      setBackend,
      setBucket,
      setObjectKey,
      setScope,
      isPickerOpen,
      openScopePicker,
      closeScopePicker,
      setPickerOpen,
    ],
  );

  return (
    <ScopeContext.Provider value={value}>{children}</ScopeContext.Provider>
  );
}

export function useScope() {
  const ctx = useContext(ScopeContext);
  if (!ctx) {
    throw new Error("useScope must be used within a ScopeProvider");
  }
  return ctx;
}
