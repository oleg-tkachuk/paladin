"use client";

// ShellContext — the console chrome's own state, fetched in one call.
//
// The badges, the background-ops drawer and the tenant context each used to
// poll their own RPC, so every page paid for the whole chrome before it
// rendered anything of its own, and a page's availability was the product of
// several services. /api/shell does that fan-out server-side and reports each
// section separately, so a health probe that is down greys one badge instead
// of taking the page with it.

import React, {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { fromJson, type JsonValue } from "@bufbuild/protobuf";

import {
  VersionInfoSchema,
  HealthInfoSchema,
  type VersionInfo,
  type HealthInfo,
} from "@/gen/paladin/iam/v1/health_service_pb";
import {
  ListOperationsResponseSchema,
  type Operation,
} from "@/gen/paladin/admin/v1/operation_service_pb";
import { anyRegistry } from "@/lib/connect/any-registry";
import { getAccessToken } from "@/lib/auth/tokenStore";
import { AUDIENCES } from "@/constants";
import { useVisiblePolling } from "@/hooks/useVisiblePolling";

export type SectionStatus = "ok" | "unavailable" | "loading";

export interface ShellSection<T> {
  status: SectionStatus;
  reason?: string;
  data: T | null;
}

interface ShellState {
  version: ShellSection<VersionInfo>;
  health: ShellSection<HealthInfo>;
  operations: ShellSection<Operation[]>;
  /** Set when the aggregate itself could not be reached at all. */
  error: string | null;
  lastUpdated: Date | null;
  refresh: () => Promise<void>;
}

const LOADING: ShellSection<never> = { status: "loading", data: null };
const POLL_INTERVAL = 10_000;

const ShellContext = createContext<ShellState | undefined>(undefined);

interface WireSection {
  status: "ok" | "unavailable";
  reason?: string;
  data?: JsonValue;
}

function decode<T>(
  raw: WireSection | undefined,
  parse: (json: JsonValue) => T,
): ShellSection<T> {
  if (!raw)
    return { status: "unavailable", reason: "missing section", data: null };
  if (raw.status !== "ok" || raw.data === undefined) {
    return { status: "unavailable", reason: raw.reason, data: null };
  }
  try {
    return { status: "ok", data: parse(raw.data) };
  } catch (err) {
    // A section that decodes to garbage is unavailable, not fatal: the rest
    // of the chrome has nothing to do with it.
    return {
      status: "unavailable",
      reason: err instanceof Error ? err.message : String(err),
      data: null,
    };
  }
}

export function ShellProvider({ children }: { children: ReactNode }) {
  const [version, setVersion] = useState<ShellSection<VersionInfo>>(LOADING);
  const [health, setHealth] = useState<ShellSection<HealthInfo>>(LOADING);
  const [operations, setOperations] =
    useState<ShellSection<Operation[]>>(LOADING);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);

  const refresh = useCallback(async () => {
    try {
      const token = await getAccessToken(AUDIENCES.admin);
      const res = await fetch("/api/shell", {
        headers: { Authorization: `Bearer ${token}` },
        cache: "no-store",
      });
      if (!res.ok) {
        throw new Error(`shell: HTTP ${res.status}`);
      }
      const body = (await res.json()) as Record<string, WireSection>;
      setVersion(decode(body.version, (j) => fromJson(VersionInfoSchema, j)));
      setHealth(decode(body.health, (j) => fromJson(HealthInfoSchema, j)));
      setOperations(
        decode(body.operations, (j) => {
          const parsed = fromJson(ListOperationsResponseSchema, j, {
            registry: anyRegistry,
          });
          return parsed.operations;
        }),
      );
      setError(null);
      setLastUpdated(new Date());
    } catch (err) {
      // The aggregate itself is down (or the token is). Every section is
      // unknown, and the chrome says so rather than showing stale badges as
      // if they were current.
      const message = err instanceof Error ? err.message : String(err);
      setError(message);
      setVersion({ status: "unavailable", reason: message, data: null });
      setHealth({ status: "unavailable", reason: message, data: null });
      setOperations({ status: "unavailable", reason: message, data: null });
    }
  }, []);

  useVisiblePolling(refresh, POLL_INTERVAL);

  const value = useMemo<ShellState>(
    () => ({ version, health, operations, error, lastUpdated, refresh }),
    [version, health, operations, error, lastUpdated, refresh],
  );

  return (
    <ShellContext.Provider value={value}>{children}</ShellContext.Provider>
  );
}

export function useShell() {
  const ctx = useContext(ShellContext);
  if (!ctx) {
    throw new Error("useShell must be used within a ShellProvider");
  }
  return ctx;
}
