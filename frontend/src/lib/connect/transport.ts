import { createConnectTransport } from "@connectrpc/connect-web";
import {
  ConnectError,
  Code,
  type Interceptor,
  type Transport,
} from "@connectrpc/connect";

import {
  RPC_API_PREFIX,
  RPC_PLANE_PREFIXES,
  AUDIENCES,
  type Plane,
  type Audience,
} from "@/constants";
import {
  configureTokenStore,
  getAccessToken,
  clearAllTokens,
} from "@/lib/auth/tokenStore";

/**
 * Multi-plane transport layer.
 *
 * Each backend plane (data/iam/admin) runs on its own port with its own JWT
 * audience. The browser hits a single Next.js BFF prefix (/api/rpc/<plane>)
 * which proxies to the in-cluster service. Clients pick a transport by the
 * plane the service belongs to — see lib/connect/client.ts.
 *
 * Tenant resolution: backend reads the tenant from the JWT `tenant` claim.
 * The UI never sends X-Tenant-ID anymore (we removed that header in the
 * 1:1 user-tenant refactor — there's no impersonation surface in the UI).
 */

const loggingInterceptor: Interceptor = (next) => async (req) => {
  try {
    return await next(req);
  } catch (err: unknown) {
    if (err instanceof ConnectError && err.code === Code.Unimplemented) {
      throw err;
    }
    if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
      // Wipe cached access tokens on 401 — the next RPC will re-fetch via
      // the BFF, which re-validates the refresh-token cookie. If that
      // also 401s the user is bounced to /login (handled at the app shell).
      clearAllTokens();
    }
    console.error(`[RPC Error] ${req.method.name}:`, err);
    throw err;
  }
};

/**
 * Hooked into tokenStore at module load.
 *
 * Resolution: POST /api/auth/exchange { audience } — the BFF reads the
 * iam refresh-token cookie set at /api/auth/login, calls
 * AuthService.ExchangeAudience for non-iam audiences (or RefreshToken
 * for iam itself), and returns a short-lived per-audience access token.
 *
 * The earlier dev-token branch (a single fixed JWT injected via
 * <meta name="paladin-dev-token"> from the YAML config) was a stopgap before
 * Login was wired. It's been removed because the dev token is minted
 * with audience "paladin-api" — not one of the three plane audiences — so
 * once login worked it started poisoning the per-plane caches and every
 * data/admin RPC failed with `[unauthenticated] jwt: audience mismatch`.
 * SREs who need to bypass Login can still inject tokens directly via
 * `setAccessToken(audience, …)` from DevTools.
 */
configureTokenStore(async (audience: Audience) => {
  const res = await fetch("/api/auth/exchange", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ audience }),
    credentials: "same-origin",
  });
  if (!res.ok) {
    throw new Error(`exchange failed (${res.status}) for audience=${audience}`);
  }
  const body = (await res.json()) as { token: string; expiresAt: number };
  return { token: body.token, expiresAt: body.expiresAt };
});

function authInterceptorFor(audience: Audience): Interceptor {
  return (next) => async (req) => {
    try {
      const token = await getAccessToken(audience);
      req.header.set("Authorization", `Bearer ${token}`);
    } catch {
      // No token available — let the request go out unauthenticated.
      // Backend will return 401, the catch below self-heals.
    }
    try {
      return await next(req);
    } catch (err) {
      // Self-heal on 401: the cached access token expired (or was
      // signed by a now-rotated key) but the refresh-token cookie is
      // still good. Wipe cache, fetch a fresh access token, retry the
      // request ONCE. Without this, polling consumers (StatsContext,
      // useAuditLogs, …) loop forever on stale tokens because each
      // failure clears the cache but the consumer just re-fires the
      // same broken request on the next tick.
      if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
        clearAllTokens();
        try {
          const fresh = await getAccessToken(audience);
          req.header.set("Authorization", `Bearer ${fresh}`);
          return await next(req);
        } catch {
          // Refresh path is also broken — fall through with the
          // original error. App shell handles the redirect.
        }
      }
      throw err;
    }
  };
}

function makePlaneTransport(plane: Plane, audience: Audience): Transport {
  return createConnectTransport({
    baseUrl: `${RPC_API_PREFIX}${RPC_PLANE_PREFIXES[plane]}`,
    interceptors: [loggingInterceptor, authInterceptorFor(audience)],
  });
}

export const dataTransport = makePlaneTransport("data", AUDIENCES.data);
export const iamTransport = makePlaneTransport("iam", AUDIENCES.iam);
export const adminTransport = makePlaneTransport("admin", AUDIENCES.admin);

/**
 * Legacy single-transport export. Old callers (pre-multi-plane refactor)
 * imported `transport` directly. It points at the data plane — services
 * outside data will 401 with audience mismatch. Migrate call sites to
 * lib/connect/client.ts which dispatches to the right transport per service.
 *
 * @deprecated import a typed client from `@/lib/connect/client` instead.
 */
export const transport = dataTransport;
