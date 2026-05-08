"use client";

import React, { createContext, useCallback, useContext, useState } from "react";

import { DEFAULT_OBJECT_KEY, STORAGE_KEYS } from "@/constants";

/**
 * Scope — three soft coordinates that locate the operator inside the
 * control plane. The hard coordinate (Tenant) lives in TenantContext
 * because it gates every RPC via the X-Tenant-ID header; everything
 * else here is opt-in and used by pages that want to share filter
 * state.
 *
 *   backend     → which storage backend the user is focused on
 *   bucket      → which physical bucket the user is focused on
 *   objectKey   → which ObjectKey namespace the user is browsing
 *                 (used by /objects, sidebar counts, command palette,
 *                 ObjectInspector — single source of truth so the badge
 *                 counts, scope picker, and CommandPalette searches
 *                 always agree)
 *
 * All three are persisted to localStorage so refresh keeps the user's
 * mental model intact. Pages that don't `useScope()` keep working
 * unchanged.
 */
interface ScopeContextType {
  backendId: string | null;
  bucketName: string | null;
  objectKey: string;
  setBackend: (id: string | null) => void;
  setBucket: (name: string | null) => void;
  setObjectKey: (key: string) => void;
  // setScope writes the backend + bucket pair atomically. Picking a
  // bucket from the switcher needs this — `setBackend` clears the
  // bucket as a safety net, so calling setBackend(b.backendId) then
  // setBucket(b.bucketName) leaves you with only the bucket in
  // localStorage. Use setScope when you already know the canonical
  // (backend, bucket) pair.
  setScope: (backendId: string | null, bucketName: string | null) => void;
}

const ScopeContext = createContext<ScopeContextType | undefined>(undefined);

const safeRead = (key: string): string | null => {
  if (typeof window === "undefined") return null;
  return localStorage.getItem(key);
};

export function ScopeProvider({ children }: { children: React.ReactNode }) {
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

  return (
    <ScopeContext.Provider
      value={{
        backendId,
        bucketName,
        objectKey,
        setBackend,
        setBucket,
        setObjectKey,
        setScope,
      }}
    >
      {children}
    </ScopeContext.Provider>
  );
}

export function useScope() {
  const ctx = useContext(ScopeContext);
  if (!ctx) {
    throw new Error("useScope must be used within a ScopeProvider");
  }
  return ctx;
}
