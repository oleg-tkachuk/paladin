// In-memory per-audience access-token cache with single-flight refresh.
//
// Why in-memory: access tokens never touch localStorage/sessionStorage to
// limit XSS blast radius; refresh tokens live in an httpOnly cookie set by
// the BFF (/api/auth/login). On page reload the store is empty — the
// transport's first request triggers a refresh against the cookie.
//
// Cross-tab sync: BroadcastChannel mirrors token updates so a Login in one
// tab populates the cache in others without each tab refreshing separately.
//
// Single-flight: parallel RPCs from one tab share a single in-flight
// refresh/exchange request; the first call dispatches, the rest await its
// promise. Otherwise N parallel RPCs would cause N refreshes and the backend
// would invalidate jti's faster than the UI could use them.

import type { Audience } from "@/constants";

type TokenEntry = {
  token: string;
  // Epoch ms when token expires. We refresh ~30s early to avoid races.
  expiresAt: number;
};

const SKEW_MS = 30_000;
const CHANNEL_NAME = "paladin-auth";

const cache = new Map<Audience, TokenEntry>();
const inflight = new Map<Audience, Promise<TokenEntry>>();
let channel: BroadcastChannel | null = null;

type ChannelMsg =
  { kind: "set"; audience: Audience; entry: TokenEntry } | { kind: "clear" };

function getChannel(): BroadcastChannel | null {
  if (typeof window === "undefined") return null;
  if (channel) return channel;
  if (typeof BroadcastChannel === "undefined") return null;
  channel = new BroadcastChannel(CHANNEL_NAME);
  channel.onmessage = (e: MessageEvent<ChannelMsg>) => {
    const msg = e.data;
    if (msg.kind === "set") cache.set(msg.audience, msg.entry);
    else if (msg.kind === "clear") cache.clear();
  };
  return channel;
}

function broadcast(msg: ChannelMsg) {
  getChannel()?.postMessage(msg);
}

function isFresh(entry: TokenEntry | undefined): entry is TokenEntry {
  return !!entry && entry.expiresAt - SKEW_MS > Date.now();
}

/**
 * Fetcher signature — supplied by the transport layer at startup so the
 * tokenStore stays decoupled from fetch URLs and BFF wiring.
 */
export type AudienceFetcher = (audience: Audience) => Promise<TokenEntry>;

let fetcher: AudienceFetcher | null = null;

export function configureTokenStore(f: AudienceFetcher) {
  fetcher = f;
}

/**
 * Returns a fresh access token for the given plane's audience. If the
 * cached one is fresh, returns it; otherwise calls the configured fetcher
 * (single-flight per audience) and caches the result.
 */
export async function getAccessToken(audience: Audience): Promise<string> {
  const cached = cache.get(audience);
  if (isFresh(cached)) return cached.token;

  const existing = inflight.get(audience);
  if (existing) return (await existing).token;

  if (!fetcher) {
    throw new Error(
      "tokenStore not configured — call configureTokenStore() at app boot",
    );
  }

  const promise = fetcher(audience)
    .then((entry) => {
      cache.set(audience, entry);
      broadcast({ kind: "set", audience, entry });
      return entry;
    })
    .finally(() => {
      inflight.delete(audience);
    });

  inflight.set(audience, promise);
  return (await promise).token;
}

/**
 * Direct insert — used by the login flow after a successful AuthService.Login
 * to seed the cache before any RPC fires.
 */
export function setAccessToken(audience: Audience, entry: TokenEntry) {
  cache.set(audience, entry);
  broadcast({ kind: "set", audience, entry });
}

/** Wipe all cached tokens (called on logout). */
export function clearAllTokens() {
  cache.clear();
  inflight.clear();
  broadcast({ kind: "clear" });
}

/**
 * Mark a single audience's cached token stale without touching the
 * inflight dedup map. Used by the transport's 401 self-heal: parallel
 * RPCs all hit Unauthenticated, all need a fresh token, but we want
 * exactly ONE refetch to fire — the inflight Map enforces that. The
 * earlier path used clearAllTokens() which dropped the inflight Map
 * too, so each parallel 401 fired its own /exchange call, each
 * clobbering the previous cache entry and (worse) racing the BFF's
 * refresh-token rotation. Separate stale-marking restores the
 * single-flight property on the recovery path.
 */
export function markAudienceStale(audience: Audience) {
  cache.delete(audience);
}

/** Test/debug helper — peek without triggering a fetch. */
export function peekAccessToken(audience: Audience): string | null {
  const entry = cache.get(audience);
  return isFresh(entry) ? entry.token : null;
}
