// The seeding helpers' cache of access tokens, one per audience.
//
// Callers ask concurrently — a teardown purges a dozen tenants with
// Promise.allSettled, and every call wants the admin token. A cache that
// remembered only finished tokens sent each of those callers to log in on
// its own, IAM's login rate limit refused the burst ("too many attempts"),
// and the teardown failed: that is how leftover e2e tenants piled up on the
// dev cluster. So a login in flight is shared: every caller for an audience
// waits on the one login, and only a failed one is tried again.

/** A token and when it lapses, in epoch ms. */
export interface CachedToken {
  token: string;
  expiresAt: number;
}

export interface TokenCacheOptions {
  /** Logs in for `audience` and returns the access token. */
  login: (audience: string) => Promise<string>;
  /** When `token` lapses, in epoch ms; 0 when that cannot be read. */
  expiry: (token: string) => number;
  /** Refresh this long before a token lapses. */
  refreshMarginMs: number;
  /** The clock, for tests. */
  now?: () => number;
}

/** A per-audience token getter that logs in at most once at a time. */
export function newTokenCache(
  opts: TokenCacheOptions,
): (audience: string) => Promise<string> {
  const now = opts.now ?? Date.now;
  const cached = new Map<string, CachedToken>();
  const inFlight = new Map<string, Promise<string>>();
  return (audience) => {
    const hit = cached.get(audience);
    if (hit && now() < hit.expiresAt - opts.refreshMarginMs) {
      return Promise.resolve(hit.token);
    }
    const pending = inFlight.get(audience);
    if (pending) return pending;
    const login = opts
      .login(audience)
      .then((token) => {
        cached.set(audience, { token, expiresAt: opts.expiry(token) });
        return token;
      })
      .finally(() => inFlight.delete(audience));
    inFlight.set(audience, login);
    return login;
  };
}

/** Read `exp` out of a JWT without verifying it — this is a test helper
 *  deciding when to re-login, not an authorization check. A token whose exp
 *  cannot be read is treated as already expired, so the next call re-logins
 *  rather than sending something unusable. */
export function jwtExpiryMs(token: string): number {
  try {
    const payload = token.split(".")[1];
    const json = JSON.parse(Buffer.from(payload, "base64url").toString("utf8"));
    return typeof json.exp === "number" ? json.exp * 1000 : 0;
  } catch {
    return 0;
  }
}
