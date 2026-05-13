"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import { type Audience } from "@/constants";
import { setAccessToken, clearAllTokens } from "@/lib/auth/tokenStore";

/**
 * AuthContext — single source of truth for the signed-in user on the client.
 *
 * On mount: hits /api/auth/me. If the BFF returns 200, we seed the
 * tokenStore with the iam access token and mark the session authenticated;
 * tokens for data/admin are fetched lazily by the transport on first RPC.
 *
 * login/logout drive the session lifecycle and keep the in-memory token
 * cache in sync. Refresh-tokens never enter this layer — they live in
 * httpOnly cookies the BFF owns.
 */

export type AuthUser = {
  userId: string;
  tenantId: string;
  // Tenant slug from the JWT `tenant_slug` claim (via WhoAmI). Empty
  // for legacy sessions minted before the slug claim wiring; callers
  // fall back to tenantId for URL routing in that case.
  tenantSlug: string;
  subject: string;
  displayName: string;
  roles: string[];
  resourceVersion: string;
};

type AccessTokenDTO = {
  audience: Audience;
  token: string;
  expiresAt: number;
};

export type AuthStatus = "loading" | "authenticated" | "unauthenticated";

type AuthContextValue = {
  user: AuthUser | null;
  status: AuthStatus;
  error: string | null;
  login: (
    subject: string,
    password: string,
    upstreamCode?: string,
  ) => Promise<void>;
  logout: () => Promise<void>;
};

const AuthContext = createContext<AuthContextValue | null>(null);

function seedTokens(tokens: AccessTokenDTO[]) {
  for (const t of tokens) {
    setAccessToken(t.audience, { token: t.token, expiresAt: t.expiresAt });
  }
}

// Module-level dedup for the on-mount rehydrate. React StrictMode mounts
// effects twice in dev; without this, both mounts call /api/auth/me in
// parallel, which races on the iam refresh-token rotation — the first
// call rotates and returns 200, the second arrives with the now-stale
// cookie and returns 401, flipping status back to "unauthenticated".
//
// Why module-scope and not a ref: the dedup must span across the two
// distinct provider instances StrictMode mounts — a ref is per-instance
// and therefore doesn't catch the race.
let rehydratePromise: Promise<
  | {
      ok: true;
      user: AuthUser;
      accessTokens: AccessTokenDTO[];
    }
  | { ok: false }
> | null = null;

function rehydrate() {
  if (rehydratePromise) return rehydratePromise;
  rehydratePromise = (async () => {
    try {
      const res = await fetch("/api/auth/me", {
        method: "GET",
        credentials: "same-origin",
      });
      if (!res.ok) return { ok: false as const };
      const body = (await res.json()) as {
        user: AuthUser;
        accessTokens: AccessTokenDTO[];
      };
      return {
        ok: true as const,
        user: body.user,
        accessTokens: body.accessTokens,
      };
    } catch {
      return { ok: false as const };
    } finally {
      // Clear after a tick so the StrictMode double-mount joins this
      // promise but a real subsequent re-mount (e.g., after logout) gets
      // a fresh fetch.
      setTimeout(() => {
        rehydratePromise = null;
      }, 0);
    }
  })();
  return rehydratePromise;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [status, setStatus] = useState<AuthStatus>("loading");
  const [error, setError] = useState<string | null>(null);

  // On mount, try to rehydrate from BFF. The fetch is deduped at module
  // scope (see rehydrate()) so StrictMode's double-mount doesn't race the
  // refresh-token rotation.
  useEffect(() => {
    let cancelled = false;
    void rehydrate().then((result) => {
      if (cancelled) return;
      if (!result.ok) {
        setStatus("unauthenticated");
        return;
      }
      seedTokens(result.accessTokens);
      setUser(result.user);
      setStatus("authenticated");
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const login = useCallback(
    async (subject: string, password: string, upstreamCode?: string) => {
      setError(null);
      setStatus("loading");
      try {
        const res = await fetch("/api/auth/login", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ subject, password, upstreamCode }),
        });
        if (!res.ok) {
          const errBody = await res
            .json()
            .catch(() => ({}) as { error?: string });
          const msg = errBody?.error || `login failed (${res.status})`;
          setError(msg);
          setStatus("unauthenticated");
          throw new Error(msg);
        }
        const body = (await res.json()) as {
          user: AuthUser;
          accessTokens: AccessTokenDTO[];
        };
        seedTokens(body.accessTokens);
        setUser(body.user);
        setStatus("authenticated");
      } catch (e) {
        setStatus("unauthenticated");
        throw e;
      }
    },
    [],
  );

  const logout = useCallback(async () => {
    try {
      await fetch("/api/auth/logout", {
        method: "POST",
        credentials: "same-origin",
      });
    } finally {
      clearAllTokens();
      setUser(null);
      setStatus("unauthenticated");
    }
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({ user, status, error, login, logout }),
    [user, status, error, login, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used within <AuthProvider>");
  }
  return ctx;
}
