"use client";

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useQuery } from "@tanstack/react-query";
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
 *               claim via WhoAmI, never from localStorage. It changes
 *               only via useAuth().switchTenant (AuthService.SwitchTenant),
 *               which re-mints the session for another tenant the subject
 *               is a member of; when it changes, the soft scope below is
 *               reset (backend/bucket/collection are tenant-specific).
 *   tenant    → full record fetched once via TenantService.GetTenant.
 *
 * Soft scope (user-selectable, persisted to localStorage):
 *   backendId → which storage backend the user is focused on
 *   bucketId→ which physical bucket the user is focused on (cleared
 *               when backendId changes — a bucket only makes sense in
 *               the context of one backend)
 *   collection → which Collection namespace the user is browsing.
 *               Single source of truth so the OK Objects/Trash tabs,
 *               sidebar counts, CommandPalette searches, and
 *               ObjectInspector always agree.
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
  bucketId: string | null;
  collection: string;
  setBackend: (id: string | null) => void;
  setBucket: (name: string | null) => void;
  setCollection: (key: string) => void;
  // setScope writes the backend + bucket pair atomically. Picking a
  // bucket from the picker needs this — `setBackend` clears the
  // bucket as a safety net, so calling setBackend(b.backendId) then
  // setBucket(b.bucketId) leaves you with only the bucket in
  // localStorage. Use setScope when you already know the canonical
  // (backend, bucket) pair.
  setScope: (backendId: string | null, bucketId: string | null) => void;

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

  // Tenant record fetched via GetTenant, cached by TanStack. `enabled` gates
  // on auth + tenantId; deriving `tenant` from auth status (below) clears it
  // on logout without a setState-in-effect.
  const tenantQuery = useQuery({
    queryKey: ["scopeTenant", tenantId],
    enabled: status === "authenticated" && !!tenantId,
    queryFn: async ({ signal }) => {
      try {
        const fetched = await tenantClient.getTenant(
          { name: `tenants/${tenantId}` },
          { signal },
        );
        return fetched ?? null;
      } catch (err) {
        // 404 / NotFound is plausible right after signup or in dev where the
        // JWT carries a tenant id that doesn't exist yet — log, return null,
        // don't red-toast.
        if (err instanceof ConnectError) {
          console.warn(
            `[ScopeContext] getTenant(${tenantId}) failed:`,
            err.rawMessage,
          );
        } else {
          console.error("[ScopeContext] getTenant failed:", err);
        }
        return null;
      }
    },
  });
  // Only surface the record while authenticated — clears on logout.
  const tenant = status === "authenticated" ? (tenantQuery.data ?? null) : null;
  const isTenantLoading = tenantQuery.isFetching;
  const refetchTenant = tenantQuery.refetch;
  const refreshTenant = useCallback(async () => {
    await refetchTenant();
  }, [refetchTenant]);

  // ── Soft scope: backend / bucket / collection ───────────────────────────
  const [backendId, setBackendIdState] = useState<string | null>(() =>
    safeRead(STORAGE_KEYS.scopeBackend),
  );
  const [bucketId, setBucketIdState] = useState<string | null>(() =>
    safeRead(STORAGE_KEYS.scopeBucket),
  );
  const [collection, setCollectionState] = useState<string>(
    () => safeRead(STORAGE_KEYS.scopeCollection) || DEFAULT_OBJECT_KEY,
  );

  const setBackend = useCallback((id: string | null) => {
    setBackendIdState(id);
    if (id) localStorage.setItem(STORAGE_KEYS.scopeBackend, id);
    else localStorage.removeItem(STORAGE_KEYS.scopeBackend);
    // Changing the backend implicitly invalidates the bucket selection
    // (a bucket only makes sense in the context of one backend).
    setBucketIdState(null);
    localStorage.removeItem(STORAGE_KEYS.scopeBucket);
  }, []);

  const setBucket = useCallback((name: string | null) => {
    setBucketIdState(name);
    if (name) localStorage.setItem(STORAGE_KEYS.scopeBucket, name);
    else localStorage.removeItem(STORAGE_KEYS.scopeBucket);
  }, []);

  const setScope = useCallback((id: string | null, name: string | null) => {
    setBackendIdState(id);
    if (id) localStorage.setItem(STORAGE_KEYS.scopeBackend, id);
    else localStorage.removeItem(STORAGE_KEYS.scopeBackend);
    setBucketIdState(name);
    if (name) localStorage.setItem(STORAGE_KEYS.scopeBucket, name);
    else localStorage.removeItem(STORAGE_KEYS.scopeBucket);
  }, []);

  const setCollection = useCallback((key: string) => {
    const normalized = key?.trim() || DEFAULT_OBJECT_KEY;
    setCollectionState(normalized);
    localStorage.setItem(STORAGE_KEYS.scopeCollection, normalized);
  }, []);

  // Reset the soft scope when the tenant changes under us (SwitchTenant).
  // backend / bucket / collection are tenant-specific, so carrying them across
  // a switch would point the UI at resources that live in the previous tenant.
  // Fires only on a genuine change (prev + next both set and different) — not
  // on the initial mount or on logout (tenantId → null).
  const prevTenantRef = useRef<string | null>(null);
  useEffect(() => {
    const prev = prevTenantRef.current;
    prevTenantRef.current = tenantId;
    if (prev && tenantId && prev !== tenantId) {
      setBackendIdState(null);
      localStorage.removeItem(STORAGE_KEYS.scopeBackend);
      setBucketIdState(null);
      localStorage.removeItem(STORAGE_KEYS.scopeBucket);
      setCollectionState(DEFAULT_OBJECT_KEY);
      localStorage.removeItem(STORAGE_KEYS.scopeCollection);
    }
  }, [tenantId]);

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
      bucketId,
      collection,
      setBackend,
      setBucket,
      setCollection,
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
      bucketId,
      collection,
      setBackend,
      setBucket,
      setCollection,
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
