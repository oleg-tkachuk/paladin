"use client";

import { useCallback, useEffect, useState } from "react";
import { ConnectError } from "@connectrpc/connect";

import { adminSystemClient } from "@/lib/connect/client";

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
  const [config, setConfig] = useState<string | null>(null);
  const [path, setPath] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await adminSystemClient.getConfig({});
      setConfig(res.yaml);
      setPath(res.sourcePath || null);
    } catch (err) {
      const e =
        err instanceof ConnectError
          ? new Error(err.rawMessage)
          : err instanceof Error
            ? err
            : new Error(String(err));
      setError(e);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { config, path, loading, error, refresh };
}
