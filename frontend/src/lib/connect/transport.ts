import { createConnectTransport } from "@connectrpc/connect-web";
import { anyRegistry } from "./any-registry";
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
 * The header does two different things on the server, and only one of them
 * looks at the method name (backend `internal/middleware/idempotency.go`):
 *
 *   - it is REQUIRED on `Create*` / `Issue*`, rejected with InvalidArgument
 *     before the handler runs when absent;
 *   - it is HONOURED on any RPC at all — a request carrying a key gets its
 *     first response memoized and replayed.
 *
 * So the interesting set is wider than the required one. `UploadObject` is the
 * case that motivated widening it: a retry after a dropped connection collides
 * on the (collection, path) unique index and answers AlreadyExists, when what
 * the caller wants — and what a key delivers — is the original object id and
 * presigned URL back.
 *
 * It is NOT every mutation, and the exclusions are the point:
 *
 *   - Reads. Memoizing a List would serve a stale page under a key the caller
 *     reused, and every Get would write an idempotency row for nothing.
 *   - `Update*` / `Set*`. Already guarded by `resource_version`: a duplicate
 *     either writes the same value or fails Aborted. A key adds a row and no
 *     safety.
 *   - `Delete*`. Idempotent by nature — the second one finds nothing to do.
 *   - Credential minting (Login, RefreshToken, ExchangeAudience, SwitchTenant).
 *     Replaying these is actively wrong: refresh tokens rotate, and a replayed
 *     response hands back a pair the server has already invalidated. The server
 *     refuses to memoize them regardless (middleware.CredentialMintingProcedures);
 *     not sending a key here keeps the two sides saying the same thing.
 *
 * A fresh UUID per call, so a transport-level retry of the same request object
 * collapses. It does NOT collapse two separate user clicks — those are two
 * calls and get two keys. Making a double-click idempotent needs a key derived
 * at the call site from the submission, which is a call-site decision, not a
 * transport one.
 */
const IDEMPOTENT_PREFIXES = [
  "Create",
  "Issue",
  "Delegate",
  "Grant",
  // Object lifecycle: each of these creates a row or backend-side state, and
  // each is reachable over a connection that can drop mid-flight.
  "Upload",
  "Complete",
  "Initiate",
  "Copy",
  "Restore",
  "Batch",
];

export function wantsIdempotencyKey(methodName: string): boolean {
  return IDEMPOTENT_PREFIXES.some((p) => methodName.startsWith(p));
}

const idempotencyInterceptor: Interceptor = (next) => async (req) => {
  if (
    !wantsIdempotencyKey(req.method.name) ||
    req.header.has("Idempotency-Key")
  ) {
    return next(req);
  }
  // Three request messages carry an `idempotency_key` FIELD of their own —
  // UploadObject, InitiateMultipartUpload, and the capability issue/delegate
  // pair. The server resolves the key from the header OR that field, and
  // rejects the call outright when both are set and differ:
  //
  //     invalid_argument: Idempotency-Key header and idempotency_key field disagree
  //
  // which is right — a caller contradicting itself is a bug, and picking a
  // winner would make the effective key depend on an undocumented precedence.
  // The first version of this interceptor stamped a fresh UUID over the field
  // the console already sets per queued file, and every console upload started
  // failing with that 400.
  //
  // So mirror the field instead of inventing a value. Both sides then say the
  // same thing, and the key is the one the CALL SITE chose — stable across a
  // real double-submit, where a per-call UUID only ever collapses a
  // transport-level retry.
  const carried = (req.message as { idempotencyKey?: unknown }).idempotencyKey;
  req.header.set(
    "Idempotency-Key",
    typeof carried === "string" && carried !== ""
      ? carried
      : crypto.randomUUID(),
  );
  return next(req);
};

// A request aborted by the caller (TanStack Query cancels a query's RPC on
// unmount / key-change; React StrictMode double-mounts make this routine in
// dev) is expected, not an error — surfaces as ConnectError Code.Canceled or
// a raw AbortError. Don't log those as `[RPC Error]`.
function isCanceled(err: unknown): boolean {
  if (err instanceof ConnectError) return err.code === Code.Canceled;
  return err instanceof DOMException && err.name === "AbortError";
}

const loggingInterceptor: Interceptor = (next) => async (req) => {
  try {
    return await next(req);
  } catch (err: unknown) {
    if (err instanceof ConnectError && err.code === Code.Unimplemented) {
      throw err;
    }
    if (isCanceled(err)) {
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

function describe(err: unknown): string {
  if (err instanceof ConnectError) return err.rawMessage;
  if (err instanceof Error) return err.message;
  return String(err);
}

function authInterceptorFor(audience: Audience): Interceptor {
  return (next) => async (req) => {
    // Why the mint error is kept rather than thrown: an unauthenticated
    // request reaching the backend is the self-heal path below, and it works.
    // But when it does NOT heal, the error the operator sees is the backend's
    // "missing Authorization header", which describes the symptom and hides
    // the cause — a revoked refresh family reads as a console that forgot to
    // send a header. One e2e failure was diagnosed twice for that reason.
    let mintErr: unknown;
    try {
      const token = await getAccessToken(audience);
      req.header.set("Authorization", `Bearer ${token}`);
    } catch (err) {
      // No token available — let the request go out unauthenticated.
      // Backend will return 401, the catch below self-heals.
      mintErr = err;
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
        } catch (retryErr) {
          // Refresh path is also broken. Report why the token could not be
          // minted — that is the actual failure — instead of the backend's
          // complaint about the header that was missing because of it.
          throw new ConnectError(
            `no ${audience} access token: ${describe(mintErr ?? retryErr)}`,
            Code.Unauthenticated,
            undefined,
            undefined,
            err,
          );
        }
      }
      throw err;
    }
  };
}

function makePlaneTransport(plane: Plane, audience: Audience): Transport {
  return createConnectTransport({
    baseUrl: `${RPC_API_PREFIX}${RPC_PLANE_PREFIXES[plane]}`,
    jsonOptions: { registry: anyRegistry },
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
