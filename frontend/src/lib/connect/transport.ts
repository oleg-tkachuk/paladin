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
  markAudienceStale,
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

/**
 * Idempotency-Key auto-injection.
 *
 * Backend enforces an `Idempotency-Key` header on every
 * mutation-shaped RPC (see backend
 * `internal/middleware/idempotency.go`, RequireOnCreate=true on
 * all three planes). The server-side matcher recognises method
 * names with prefix `Create` (AIP-canonical: CreateTenant,
 * CreateBucket, …) AND `Issue` (the token/credential variant:
 * CapabilityService.Issue). A missing header gets rejected with
 * InvalidArgument before the handler runs. To keep call sites
 * unaware of this, the transport auto-injects a fresh UUID on
 * any RPC whose method name starts with either prefix, unless
 * the caller already set one (e.g. an explicit retry that wants
 * to collapse onto the original key).
 *
 * Why UUID v4 (crypto.randomUUID) and not v7: v7 needs an extra
 * dep and the embedded timestamp gives the server nothing useful
 * here — the key is opaque to the server. v4's 122-bit space is
 * more than enough collision-resistance.
 *
 * Scope: only Create* / Issue*. List/Get/Update/Delete are
 * either idempotent by definition (reads) or already keyed by
 * their resource ID (updates/deletes carry the ID). The two
 * sides MUST stay in sync — if the server adds a new prefix
 * (e.g. "Submit*"), this matcher needs the same update.
 */
const idempotencyInterceptor: Interceptor = (next) => async (req) => {
  const name = req.method.name;
  if (
    (name.startsWith("Create") || name.startsWith("Issue")) &&
    !req.header.has("Idempotency-Key")
  ) {
    req.header.set("Idempotency-Key", crypto.randomUUID());
  }
  return next(req);
};

const loggingInterceptor: Interceptor = (next) => async (req) => {
  try {
    return await next(req);
  } catch (err: unknown) {
    if (err instanceof ConnectError && err.code === Code.Unimplemented) {
      throw err;
    }
    // No token-cache work here — the auth interceptor (inner) owns
    // the 401 self-heal path. An earlier version eagerly cleared
    // the entire cache on every 401 even after the auth interceptor
    // had already self-healed; that wiped fresh tokens for OTHER
    // audiences and forced redundant /exchange round-trips on every
    // subsequent RPC.
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
      // still good. Mark THIS audience stale, fetch a fresh access
      // token (single-flight via inflight Map), retry once.
      //
      // Don't use clearAllTokens here: it drops the inflight Map too,
      // so parallel 401s would each fire their own /exchange call
      // against the BFF and clobber each other's cache writes —
      // which then races the BFF's refresh-token cookie rotation
      // and intermittently wedges the page (the symptom that lives
      // on as "page sometimes blank on refresh").
      if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
        markAudienceStale(audience);
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
    // Order: idempotency runs first (header injection happens
    // before auth/logging see the request). Auth has to wrap the
    // network call so 401 self-heal works; logging is outermost so
    // it observes the final outcome.
    interceptors: [
      idempotencyInterceptor,
      loggingInterceptor,
      authInterceptorFor(audience),
    ],
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
