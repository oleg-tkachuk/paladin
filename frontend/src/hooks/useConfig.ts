"use client";

import { useQuery } from "@tanstack/react-query";

import { adminSystemClient } from "@/lib/connect/client";
import { normalizeError } from "@/lib/connect/error";

// useConfig — wraps admin/v1.SystemService.GetConfig.
//
// Returns the running control-plane config as YAML with secrets
// already redacted to "***" by the backend (see Config.Obfuscated()
// in internal/config/resolver.go). Cheap on the server side — no DB
// hit — so callers can safely refetch on demand.
//
// Errors are surfaced as `error` rather than thrown so the caller
// (the /config page, plus any future surface) can render an
// informative state instead of unmounting.

export function useConfig() {
  const query = useQuery({
    queryKey: ["admin", "config"],
    queryFn: async () => {
      try {
        const res = await adminSystemClient.getConfig({});
        return { config: res.yaml, path: res.sourcePath || null };
      } catch (err) {
        throw normalizeError(err);
      }
    },
  });

  return {
    config: query.data?.config ?? null,
    path: query.data?.path ?? null,
    // isFetching (not isLoading) preserves the old hook's "loading is true
    // on mount AND on every refresh" semantics.
    loading: query.isFetching,
    error: (query.error as Error | null) ?? null,
    refresh: async () => {
      await query.refetch();
    },
  };
}
