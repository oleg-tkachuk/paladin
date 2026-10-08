import { createConnectTransport } from "@connectrpc/connect-web";
import { anyRegistry } from "./any-registry";
import {
  ConnectError,
  Code,
  type Interceptor,
  type Transport,
} from "@connectrpc/connect";

import { kNotFoundIsAnswer } from "./expected";
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

const HTTP_FORBIDDEN = 403;

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
 * Idempotency-Key auto-injection, decided by the wire contract.
 *
 * This was a prefix list — Create, Issue, Delegate, Grant, Upload, Complete,
 * Initiate, Copy, Restore, Batch — duplicated verbatim in
 * `backend/cmd/seed-fixture/main.go` and `backend/internal/mcp/idempotency.go`,
 * each with a comment telling the reader to change the other two. The proto now
 * carries the answer as `idempotency_level` on 112 of 142 RPCs, and
 * `@bufbuild/protobuf` exposes it at runtime as `req.method.idempotency`.
 *
 * The rule: stamp a key iff the method declares IDEMPOTENCY_UNKNOWN.
 *
 *   - NO_SIDE_EFFECTS changes nothing. A key would write a row in
 *     idempotency_keys and risk serving a snapshot from up to a TTL ago in
 *     answer to a question about now.
 *   - IDEMPOTENT is already safe to repeat — by resource_version, by being a
 *     removal, or by upserting the value the caller supplied. A key adds a row
 *     and no safety.
 *   - IDEMPOTENCY_UNKNOWN is every call whose repeat nobody has promised
 *     anything about, which is exactly where the key does its work.
 *
 * Two behaviours change, and both are the descriptor being right where the
 * prefixes were not. `RestoreObjectVersion` no longer gets a key: the `Restore`
 * prefix swept it in, and it is OCC-guarded. Credential minting — Login,
 * RefreshToken, ExchangeAudience, SwitchTenant — now DOES get one: the console
 * used to withhold it because replaying a rotated refresh token is wrong, which
 * is true and is enforced where it belongs, on the server
 * (middleware.CredentialMintingProcedures). A key is a caller's de-duplication
 * token, not a request to cache; the server decides, and for those four it
 * declines.
 *
 * A fresh UUID per call, so a transport-level retry of the same request object
 * collapses. It does NOT collapse two separate user clicks — those are two
 * calls and get two keys. Making a double-click idempotent needs a key derived
 * at the call site from the submission, which is a call-site decision, not a
 * transport one.
 */
export function wantsIdempotencyKey(method: { idempotency?: number }): boolean {
  // 0 is IDEMPOTENCY_UNKNOWN in google.protobuf.MethodOptions. A method
  // descriptor without the field at all is treated the same way: the generated
  // code always sets it, so `undefined` means the caller passed something that
  // is not a method descriptor, and defaulting to "send a key" keeps a
  // mis-wired call safe rather than silently unkeyed.
  return (method.idempotency ?? 0) === 0;
}

const idempotencyInterceptor: Interceptor = (next) => async (req) => {
  if (!wantsIdempotencyKey(req.method) || req.header.has("Idempotency-Key")) {
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
    if (
      err instanceof ConnectError &&
      err.code === Code.NotFound &&
      req.contextValues.get(kNotFoundIsAnswer)
    ) {
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
 * Login was wired. It was removed because the dev token was then minted
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
  // 403: IAM will not issue this audience to this principal — a pure
  // tenant.user asking for paladin-admin. Typed, so the auth interceptor can
  // fail the call rather than send it and re-mint on the 401.
  if (res.status === HTTP_FORBIDDEN) {
    const body = (await res.json().catch(() => ({}))) as { error?: string };
    throw new ConnectError(
      body.error || `audience ${audience} refused`,
      Code.PermissionDenied,
    );
  }
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
      // Refused, not missing: no retry can change the answer.
      if (err instanceof ConnectError && err.code === Code.PermissionDenied) {
        throw err;
      }
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
