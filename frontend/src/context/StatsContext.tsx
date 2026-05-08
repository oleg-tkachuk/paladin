"use client";

import React, {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  type ReactNode,
} from "react";
import { systemClient } from "@/lib/connect/client";
import type { VersionInfo, HealthInfo } from "@/lib/connect/system";

interface StatsData {
  version: VersionInfo;
  health: HealthInfo;
}

interface StatsContextType {
  stats: StatsData | null;
  lastUpdated: Date | null;
  loading: boolean;
  error: Error | null;
  refresh: () => Promise<StatsData | null>;
}

const StatsContext = createContext<StatsContextType | undefined>(undefined);

const POLL_INTERVAL = 10_000; // 10 seconds

export function StatsProvider({ children }: { children: ReactNode }) {
  const [stats, setStats] = useState<StatsData | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const fetchStats = useCallback(async (): Promise<StatsData | null> => {
    try {
      setLoading(true);
      const [version, health] = await Promise.all([
        systemClient.getVersion({}),
        systemClient.getHealth({}),
      ]);
      const data = { version, health };
      setStats(data);
      setLastUpdated(new Date());
      setError(null);
      return data;
    } catch (err: unknown) {
      console.error("Failed to fetch stats", err);
      const e = err instanceof Error ? err : new Error(String(err));
      setError(e);
      return null;
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchStats();
    const interval = setInterval(fetchStats, POLL_INTERVAL);
    return () => clearInterval(interval);
  }, [fetchStats]);

  return (
    <StatsContext.Provider
      value={{ stats, lastUpdated, loading, error, refresh: fetchStats }}
    >
      {children}
    </StatsContext.Provider>
  );
}

/**
 * Hook to consume centralized system stats.
 * Eliminates duplicate polling — all consumers share a single interval.
 */
export function useStats() {
  const ctx = useContext(StatsContext);
  if (!ctx) {
    throw new Error("useStats must be used within a StatsProvider");
  }
  return ctx;
}
