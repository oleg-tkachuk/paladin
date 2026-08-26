"use client";

// StatsContext — the version + health half of the shell, in the shape its
// consumers already read.
//
// It used to own two RPCs and a 10s poller of its own. Both now arrive in the
// single /api/shell call (see ShellContext), which is what makes a page's cost
// one request instead of one per chrome widget; this stays as the adapter so
// the /health page, the dashboard and RealTimeStatus keep their imports.

import React, {
  createContext,
  useContext,
  useMemo,
  type ReactNode,
} from "react";

import { useShell, type SectionStatus } from "@/context/ShellContext";
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
  /** Per-section state, so a caller can tell "down" from "not yet". */
  versionStatus: SectionStatus;
  healthStatus: SectionStatus;
  /** Why the health section is unavailable, when it is. */
  healthReason?: string;
}

const StatsContext = createContext<StatsContextType | undefined>(undefined);

export function StatsProvider({ children }: { children: ReactNode }) {
  const shell = useShell();

  const value = useMemo<StatsContextType>(() => {
    // Both halves are needed to satisfy the StatsData shape consumers read;
    // either one missing means "no stats", with the section statuses saying
    // which one and why.
    const stats =
      shell.version.data && shell.health.data
        ? { version: shell.version.data, health: shell.health.data }
        : null;
    const loading =
      shell.version.status === "loading" || shell.health.status === "loading";
    const reason = shell.health.reason ?? shell.version.reason;
    return {
      stats,
      lastUpdated: shell.lastUpdated,
      loading,
      error: !stats && !loading && reason ? new Error(reason) : null,
      refresh: async () => {
        await shell.refresh();
        return null;
      },
      versionStatus: shell.version.status,
      healthStatus: shell.health.status,
      healthReason: shell.health.reason,
    };
  }, [shell]);

  return (
    <StatsContext.Provider value={value}>{children}</StatsContext.Provider>
  );
}

/**
 * Hook to consume system version + health. One poller feeds every consumer.
 */
export function useStats() {
  const ctx = useContext(StatsContext);
  if (!ctx) {
    throw new Error("useStats must be used within a StatsProvider");
  }
  return ctx;
}
