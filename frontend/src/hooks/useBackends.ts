"use client";

import { useCallback, useEffect, useState } from "react";
import { ConnectError } from "@connectrpc/connect";

import { backendClient } from "@/lib/connect/client";
import type { StorageBackend } from "@/gen/paladin/admin/v1/types_pb";
import { useRefreshSignal } from "@/context/RefreshContext";
import { API_PAGE_SIZE_MAX } from "@/constants";

// useBackends — list rows from the `backends` table via
// admin/v1.BackendService.ListBackends. Use this for any UI that needs
// the set of bind-targets (bucket creation, ObjectKey provisioning, etc.).
// Do NOT parse `storage.backends` from yaml config: bucket FK points at
// this table, so config-only entries can't actually receive Buckets and
// will trip `buckets_backend_id_fkey` on insert.

export function useBackends(autoFetch: boolean = true) {
  const [backends, setBackends] = useState<StorageBackend[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

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
      const msg =
        err instanceof ConnectError
          ? err.rawMessage
          : "Failed to fetch backends";
      setError(msg);
      throw err;
    } finally {
      setLoading(false);
    }
  }, []);

  const refreshSignal = useRefreshSignal("backends");
  useEffect(() => {
    if (autoFetch) void fetchBackends();
  }, [autoFetch, fetchBackends, refreshSignal]);

  return { backends, loading, error, fetchBackends };
}
