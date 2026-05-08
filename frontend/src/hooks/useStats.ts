"use client";

import { useState, useEffect, useCallback } from "react";
import { systemClient } from "@/lib/connect/client";
import type { VersionInfo, HealthInfo } from "@/lib/connect/system";
import { useNotification } from "@/components/ui/Notification";

export function useStats() {
  const { showNotification } = useNotification();
  const [stats, setStats] = useState<{
    version: VersionInfo;
    health: HealthInfo;
  } | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const fetchStats = useCallback(async () => {
    try {
      setLoading(true);
      const [version, health] = await Promise.all([
        systemClient.getVersion({}),
        systemClient.getHealth({}),
      ]);
      setStats({ version, health });
      setLastUpdated(new Date());
      setError(null);
      return { version, health };
    } catch (err: unknown) {
      console.error("Failed to fetch stats", err);
      const error = err instanceof Error ? err : new Error(String(err));
      setError(error);
      showNotification({
        type: "error",
        title: "Sync Failed",
        message: error.message || "Could not synchronize system statistics.",
      });
      return null;
    } finally {
      setLoading(false);
    }
  }, [showNotification]);

  useEffect(() => {
    fetchStats();

    const interval = setInterval(fetchStats, 10000);
    return () => clearInterval(interval);
  }, [fetchStats]);

  return {
    stats,
    lastUpdated,
    loading,
    error,
    refresh: fetchStats,
  };
}
