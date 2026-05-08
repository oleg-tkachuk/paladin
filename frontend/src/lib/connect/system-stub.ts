// Placeholder for SystemService.
// Backend RPC was removed during proto refactor; new SystemService is on the
// roadmap (decision: add to backend before UI wiring). Until then, this stub
// keeps tsc green. Runtime calls throw a clear error caught by existing
// try/catch + notification flow in useStats.

export type ComponentHealth = {
  name: string;
  status: string;
  message?: string;
  latencyMs?: number;
};

export type HealthInfo = {
  status: string;
  components?: ComponentHealth[];
};

export type VersionInfo = {
  version: string;
  commit?: string;
  buildDate?: string;
  goVersion?: string;
  // Old proto exposed buildTime as Timestamp ({seconds, nanos}); kept loose
  // so legacy AnalyticsDashboard code reading `.seconds` still type-checks.
  buildTime?: { seconds: bigint | number; nanos?: number };
};

const NOT_WIRED = "SystemService not wired yet — pending backend RPC";

/* eslint-disable @typescript-eslint/no-explicit-any */
export const systemClient = {
  getVersion: async (..._args: any[]): Promise<VersionInfo> => {
    throw new Error(NOT_WIRED);
  },
  getHealth: async (..._args: any[]): Promise<HealthInfo> => {
    throw new Error(NOT_WIRED);
  },
  getConfig: async (..._args: any[]): Promise<any> => {
    throw new Error(NOT_WIRED);
  },
};
