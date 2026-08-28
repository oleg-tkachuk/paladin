import { createClient, type Client } from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-web";
import { cookies } from "next/headers";
import type { NextResponse } from "next/server";

import { AUDIENCES, type Audience } from "@/constants";
import { AuthService } from "@/gen/paladin/iam/v1/auth_service_pb";
import { refreshCookieName } from "./cookies";

export { refreshCookieName };

/**
 * Server-side helpers for the BFF auth routes (/api/auth/*).
 *
 * The BFF holds exactly ONE refresh-token chain — bound to the iam
 * audience — and uses AuthService.ExchangeAudience to derive short-lived
 * access tokens for the data and admin planes on demand. The single
 * cookie is `paladin_rt_iam`. The Helm chart's bootstrap admin doesn't care
 * which audience the chain starts on; iam is just the conventional home.
 *
 * One cookie keeps the BFF state minimal — the data/admin chains the v1
 * design carried as separate cookies are gone.
 */

const IAM_BACKEND_URL = process.env.PALADIN_IAM_URL || "http://paladin-core:8085";

let iamClientCache: Client<typeof AuthService> | null = null;

export function iamAuthClient(): Client<typeof AuthService> {
  if (iamClientCache) return iamClientCache;
  const transport = createGrpcWebTransport({ baseUrl: IAM_BACKEND_URL });
  iamClientCache = createClient(AuthService, transport);
  return iamClientCache;
}

// ────────────────────────── Cookie helpers ──────────────────────────

// Path "/" so the proxy middleware (proxy.ts) sees the cookie on every
// route, not just /api/auth/*. The earlier narrow scope of /api/auth
// looked tighter on paper but in practice meant the middleware had no
// signal to distinguish "logged in" from "anonymous" on /, /buckets,
// etc., so it redirected logged-in users straight back to /login —
// classic post-login redirect loop. The cookie is httpOnly + SameSite=
// Strict so widening the path doesn't add JS-access surface.
const COOKIE_PATH = "/";

/** The single refresh-token cookie — pinned to the iam audience. */
export const SESSION_COOKIE_NAME = refreshCookieName(AUDIENCES.iam);

type CookieOpts = {
  maxAgeSeconds: number;
};

const baseCookieAttrs = (maxAge: number) => ({
  httpOnly: true,
  sameSite: "strict" as const,
  secure: process.env.NODE_ENV === "production",
  path: COOKIE_PATH,
  maxAge,
});

/**
 * Attach the session cookie to a NextResponse.
 *
 * The earlier `cookies().set()` form (next/headers) is fine for the
 * very first response on a request (Next.js merges its mutations into
 * the auto-built response), but it stops propagating reliably when the
 * handler builds a NextResponse explicitly via NextResponse.json() —
 * the response's own cookie jar is the only thing the framework
 * serialises, so the merged jar gets dropped silently. Symptom: rotated
 * cookies in /api/auth/exchange (audience=paladin-iam) and /api/auth/me
 * never reached the browser, leading to "no session cookie" on the
 * very next call. Writing through `res.cookies.set()` is the
 * unambiguous form.
 */
export function setSessionCookie(
  res: NextResponse,
  refreshToken: string,
  opts: CookieOpts,
) {
  res.cookies.set(
    SESSION_COOKIE_NAME,
    refreshToken,
    baseCookieAttrs(opts.maxAgeSeconds),
  );
}

export async function readSessionCookie(): Promise<string | null> {
  const jar = await cookies();
  return jar.get(SESSION_COOKIE_NAME)?.value ?? null;
}

export function clearSessionCookie(res: NextResponse) {
  res.cookies.set(SESSION_COOKIE_NAME, "", baseCookieAttrs(0));
}

// ────────────────────────── RefreshToken dedup ──────────────────────────

/**
 * AuthService.RefreshToken rotates the refresh chain — the supplied
 * token is consumed and a new one is issued. That makes concurrent
 * calls with the same input token catastrophic: the second one sees
 * a "refresh token rejected" because its parent has already been
 * replaced. In the BFF this happens on every page load — /api/auth/me
 * fires from AuthProvider, and the iamTransport interceptor often
 * fires /api/auth/exchange (audience=paladin-iam) at the same instant
 * for an early RPC. Both call RefreshToken; whichever loses the race
 * sees a 401 → AuthContext flips to unauthenticated → tenant badge
 * shows "no tenant" even though the session is valid.
 *
 * Dedup keyed on the incoming refresh-token value: while one
 * rotation is in flight, sibling callers join the same Promise and
 * receive the same new pair. The map self-cleans on settle so a
 * crashed Next.js process doesn't leak memory between requests.
 *
 * Single-instance assumption — fine for dev and small deployments.
 * Multi-replica BFF would need a shared cache (Redis), but that's
 * out of scope until the deployment topology actually changes.
 */

type RotateResult = {
  accessToken: string;
  refreshToken: string;
  accessExpiresInSeconds: number;
  refreshExpiresInSeconds: number;
};

const inflightRotations = new Map<string, Promise<RotateResult>>();

// How long a SUCCESSFUL rotation's result is retained in the dedup map AFTER it
// settles. The original dedup deleted the entry on settle, so it only collapsed
// callers that overlapped the in-flight window. But the two BFF rotation entry
// points — /api/auth/me (AuthProvider) and /api/auth/exchange?audience=paladin-iam
// (the iam transport, fired by an early iam-plane RPC) — are dispatched by the
// browser near-simultaneously carrying the SAME cookie, and the browser can't
// update that cookie between them. Under load they stagger: the first rotates
// RT1→RT2 and clears the entry; the second then replays the now-consumed RT1.
// The backend's RefreshToken reads a consumed token as RFC-6819 reuse and
// revokes the ENTIRE family (RT2 included) — logging the live session out (the
// "blank page on refresh" symptom; reproduced reliably by the specs/001
// Playwright suite under parallel workers). Retaining the resolved pair for a
// grace window lets the staggered sibling JOIN the cached promise and receive
// RT2 instead of replaying RT1. The key is (audience, token), so the next
// legitimate rotation (new cookie) uses a different key and is unaffected; a
// genuine replay outside the window still hits the backend and is detected.
const ROTATION_RESULT_GRACE_MS = 30_000;

// dedupWithGrace runs factory at most once per key for concurrent callers AND
// for callers that arrive within graceMs after a SUCCESSFUL settle. Failures
// are never cached — a rejected attempt is removed immediately so the next
// caller retries and a genuine reuse/expiry still surfaces. Exported for tests.
export function dedupWithGrace<T>(
  map: Map<string, Promise<T>>,
  key: string,
  graceMs: number,
  factory: () => Promise<T>,
): Promise<T> {
  const existing = map.get(key);
  if (existing) return existing;
  const p = factory();
  map.set(key, p);
  p.then(
    () => {
      const timer = setTimeout(() => map.delete(key), graceMs);
      (timer as { unref?: () => void }).unref?.();
    },
    () => {
      map.delete(key);
    },
  );
  return p;
}

// The rotation-successor index that used to live here is gone. It existed so
// an ExchangeAudience whose token a sibling rotation had just consumed could
// follow the chain forward — knowledge this process kept because the server
// could not tell "rotated a moment ago by its own holder" from "revoked for
// cause". It can now (refresh_tokens.superseded_at), and it honours the former
// inside a short window, so no client has to remember rotations at all.
//
// What remains below is the DEDUP, which is a different problem: two
// concurrent rotations of the same token, where the second is a genuine second
// rotation rather than a read. That one still keeps this process single-replica.

export async function refreshIamChain(
  refreshToken: string,
  audience: Audience,
): Promise<RotateResult> {
  const p = dedupWithGrace(
    inflightRotations,
    `${audience}::${refreshToken}`,
    ROTATION_RESULT_GRACE_MS,
    async () => {
      const res = await iamAuthClient().refreshToken({
        refreshToken,
        requestedAudience: audience,
      });
      if (!res.tokens) {
        throw new Error("IAM RefreshToken returned no token pair");
      }
      return {
        accessToken: res.tokens.accessToken,
        refreshToken: res.tokens.refreshToken,
        accessExpiresInSeconds: Number(res.tokens.accessExpiresInSeconds),
        refreshExpiresInSeconds: Number(res.tokens.refreshExpiresInSeconds),
      };
    },
  );
  return p;
}

// ────────────────────────── DTO shaping ──────────────────────────

/**
 * Browser-facing user payload. Strips proto-runtime fields ($typeName,
 * Timestamp objects) so the AuthContext can JSON.stringify it freely.
 *
 * `tenantSlug` is sourced from the JWT's `tenant_slug` claim via
 * WhoAmIResponse.tenant_slug — populated separately from the User
 * proto (which carries only tenant_id). The /api/auth/me handler
 * splices it in at DTO time.
 */
export type UserDTO = {
  userId: string;
  tenantId: string;
  tenantSlug: string;
  subject: string;
  displayName: string;
  roles: string[];
  resourceVersion: string;
};

export function toUserDTO(
  u: {
    userId: string;
    tenantId: string;
    subject: string;
    displayName: string;
    roles: string[];
    resourceVersion: string;
  },
  tenantSlug = "",
): UserDTO {
  return {
    userId: u.userId,
    tenantId: u.tenantId,
    tenantSlug,
    subject: u.subject,
    displayName: u.displayName,
    roles: u.roles,
    resourceVersion: u.resourceVersion,
  };
}

export type AccessTokenDTO = {
  audience: Audience;
  token: string;
  // Epoch ms when the token expires; the client stores it in tokenStore so
  // refresh fires ~30s ahead of the deadline.
  expiresAt: number;
};

export function toAccessTokenDTO(
  audience: Audience,
  accessToken: string,
  accessExpiresInSeconds: number | bigint,
): AccessTokenDTO {
  const ttl = Number(accessExpiresInSeconds);
  return {
    audience,
    token: accessToken,
    expiresAt: Date.now() + ttl * 1000,
  };
}
