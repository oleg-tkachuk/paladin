"use client";

import { useCallback, useEffect, useState } from "react";
import { create } from "@bufbuild/protobuf";
import { ConnectError } from "@connectrpc/connect";

import { backendClient } from "@/lib/connect/client";
import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import { StorageBackendSchema, StorageKind } from "@/gen/paladin/admin/v1/types_pb";
import { useBumpRefresh, useRefreshSignal } from "@/context/RefreshContext";
import { API_PAGE_SIZE_MAX } from "@/constants";

// useBackends — list rows from the `backends` table via
// admin/v1.BackendService.ListBackends. Use this for any UI that needs
// the set of bind-targets (bucket creation, ObjectKey provisioning, etc.).
// Do NOT parse `storage.backends` from yaml config: bucket FK points at
// this table, so config-only entries can't actually receive Buckets and
// will trip `buckets_backend_id_fkey` on insert.

export interface CreateBackendInput {
  backendId: string;
  displayName: string;
  kind: StorageKind;
  endpoint: string;
  publicEndpoint?: string;
  region: string;
  forcePathStyle: boolean;
  credentialsSecretRef: string;
}

export function useBackends(autoFetch: boolean = true) {
  const [backends, setBackends] = useState<StorageBackend[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const bumpRefresh = useBumpRefresh();

  const fetchBackends = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await backendClient.listBackends({
        page: { pageSize: API_PAGE_SIZE_MAX, pageToken: "" },
        filter: "",
      });
      setBackends(res.backends);
      return res.backends;
    } catch (err) {
      // Query contract (state-only): surface via `error`, never throw.
      setError(
        err instanceof ConnectError
          ? err.rawMessage
          : "Failed to fetch backends",
      );
      return [];
    } finally {
      setLoading(false);
    }
  }, []);

  // createBackend — wraps BackendService.CreateBackend. The proto's
  // Create takes the backend_id at the top level and a nested
  // StorageBackend resource carrying the rest of the fields. Tenants
  // and buckets cannot exist without a registered backend, so this
  // is the entry point for first-run platform bootstrap and any
  // later expansion to a new region/cluster.
  const createBackend = useCallback(
    async (input: CreateBackendInput): Promise<StorageBackend> => {
      try {
        setError(null);
        const backend = create(StorageBackendSchema, {
          backendId: input.backendId,
          displayName: input.displayName,
          kind: input.kind,
          endpoint: input.endpoint,
          publicEndpoint: input.publicEndpoint || "",
          region: input.region,
          forcePathStyle: input.forcePathStyle,
          credentialsSecretRef: input.credentialsSecretRef,
        });
        const created = await backendClient.createBackend({
          backendId: input.backendId,
          backend,
        });
        setBackends((prev) => [...prev, created]);
        bumpRefresh("backends");
        return created;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  // setBackendEnabled — flips a backend's enable/disable state via
  // BackendService.SetBackendEnabled. OCC-guarded: pass the backend's
  // current resourceVersion. The server refuses to disable the configured
  // default backend (FailedPrecondition) and rejects a stale version
  // (Aborted) — callers should surface err.rawMessage to the operator.
  const setBackendEnabled = useCallback(
    async (
      backendId: string,
      enabled: boolean,
      resourceVersion: string,
    ): Promise<StorageBackend> => {
      try {
        setError(null);
        const updated = await backendClient.setBackendEnabled({
          name: `storageBackends/${backendId}`,
          enabled,
          resourceVersion,
        });
        setBackends((prev) =>
          prev.map((b) => (b.backendId === backendId ? updated : b)),
        );
        bumpRefresh("backends");
        return updated;
      } catch (err) {
        // Mutation contract (throw-only): caller surfaces via errorMessage().
        throw err;
      }
    },
    [bumpRefresh],
  );

  const refreshSignal = useRefreshSignal("backends");
  useEffect(() => {
    // Fetch-on-mount / on-refresh: this is a deliberate sync with an
    // external system (the BackendService list), not derived state.
    // fetchBackends toggles loading internally; the set-state-in-effect
    // rule is a false positive for this data-loading pattern.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (autoFetch) void fetchBackends();
  }, [autoFetch, fetchBackends, refreshSignal]);

  return {
    backends,
    loading,
    error,
    fetchBackends,
    createBackend,
    setBackendEnabled,
  };
}
