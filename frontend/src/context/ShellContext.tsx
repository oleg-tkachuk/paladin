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
  useEffect,
  useRef,
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
import { canUseAdminPlane } from "@/constants/roles";
import { useAuth } from "@/context/AuthContext";
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
  // IAM never issues a pure tenant.user the admin audience; asking on every
  // poll only collects a refusal. The aggregate serves the iam sections alone.
  const { user } = useAuth();
  const adminAllowed = canUseAdminPlane(user?.roles ?? []);
  // Who the shell is for; null until someone is signed in. The provider is
  // mounted on the login page too, where asking only collects a 401 — and the
  // chrome then read "Unknown" after sign-in until the next poll.
  const session = user ? `${user.tenantId ?? ""}/${user.userId ?? ""}` : null;
  const inFlight = useRef<Promise<void> | null>(null);

  const fetchShell = useCallback(async () => {
    try {
      // Two tokens: the sections live on two planes and each plane verifies
      // its own audience. The iam one is allowed to fail on its own — that
      // costs the version and health sections, not the operations list.
      //
      // Sequential, not Promise.all. Minting a token for an audience rotates
      // the single refresh token in the session cookie, and the BFF's dedup
      // is keyed on (audience, token) — so two mints for DIFFERENT audiences
      // starting from the same cookie both send the same refresh token, the
      // loser replays a consumed one, and the backend reads that as RFC-6819
      // reuse and revokes the whole family. That logs the operator out. Only
      // the first poll of a cold session pays for the extra round trip; the
      // token store caches both afterwards.
      const adminToken = adminAllowed
        ? await getAccessToken(AUDIENCES.admin)
        : null;
      const iamToken = await getAccessToken(AUDIENCES.iam).catch(() => null);
      const res = await fetch("/api/shell", {
        headers: {
          ...(adminToken ? { Authorization: `Bearer ${adminToken}` } : {}),
          ...(iamToken
            ? { "X-Paladin-Iam-Authorization": `Bearer ${iamToken}` }
            : {}),
        },
        cache: "no-store",
      });
      if (!res.ok) {
        throw new Error(`shell: HTTP ${res.status}`);
      }
      // A session whose refresh cookie has expired gets redirected to the
      // login page by the edge middleware, and fetch follows the redirect —
      // so "200 OK" here can be an HTML page. Say that, rather than letting
      // the JSON parser report a stray "<".
      if (!res.headers.get("content-type")?.includes("application/json")) {
        throw new Error("shell: not signed in");
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
  }, [adminAllowed]);

  // One fetch at a time. The poll and the sign-in refresh below can land in
  // the same tick, and two fetches minting tokens for different audiences
  // from one cookie is the refresh-token replay the comment in fetchShell
  // describes — it revokes the session.
  const refresh = useCallback(() => {
    if (!session) return Promise.resolve();
    inFlight.current ??= fetchShell().finally(() => {
      inFlight.current = null;
    });
    return inFlight.current;
  }, [fetchShell, session]);

  useVisiblePolling(refresh, POLL_INTERVAL);

  // Ask as soon as someone is signed in, not on the next tick.
  useEffect(() => {
    void refresh();
  }, [refresh]);

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
