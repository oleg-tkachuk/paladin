/**
 * UUID-suffixed identifier helpers — the per-test isolation
 * primitive. See spec.md FR-002, plan.md "Database isolation",
 * research.md R-003.
 *
 * The test stack runs ONE shared Postgres for the entire suite.
 * Tests run in parallel (Playwright default ~4 workers). With slug
 * + display_name UNIQUE constraints from migration 033, two tests
 * that both try to create "Acme E2E" would collide. The fix is
 * trivially cheap: every seeded identifier gets an 8-hex-char
 * suffix derived from `crypto.randomUUID()`. 8 hex = 32 bits = ~4.3
 * billion possibilities, well beyond the birthday-collision
 * threshold for ≤4 concurrent workers.
 *
 * crypto.randomUUID() is built into Node 24 — no extra dep.
 */

/**
 * The shape `uniqueSlug` produces, as a matcher.
 *
 * It is exported because something has to be able to recognise a seeded row
 * after the fact — the environment guard counts leftovers with it, and the
 * suite's prefixes are far too varied to enumerate ("acme", "ok", "obj",
 * "big", "key", "sse", "e2e-user", plus whatever a caller passes). The
 * suffix is the part every seeded slug shares, because one function mints
 * them all.
 *
 * That made the convention worth pinning rather than assuming: the guard
 * asserts a freshly minted slug still matches this before it counts
 * anything, so changing the generator fails loudly at the start of a run
 * instead of quietly making the guard blind.
 */
export const FIXTURE_SLUG_RE = /-[0-9a-f]{8}$/;

/**
 * Returns `<prefix>-<8 hex chars>`, suitable for slugs (Postgres
 * citext + the regex `^[a-z0-9][a-z0-9_-]{1,62}[a-z0-9]$` checked
 * in tenant.Handler.validateSlug). Prefix MUST be lowercase /
 * hyphen-safe; helper does not sanitise.
 */
export function uniqueSlug(prefix: string): string {
  return `${prefix}-${randomHex(8)}`;
}

/**
 * Returns `<prefix> <8 hex chars>` — space-separated, suitable for
 * human-facing display names that allow whitespace and mixed case.
 */
export function uniqueDisplayName(prefix: string): string {
  return `${prefix} ${randomHex(8)}`;
}

/**
 * Internal — takes the first `n` hex chars of a UUID v4. UUID v4 is
 * cryptographically random by spec; slicing chars from it is
 * equivalent to drawing `n*4` random bits. We don't pretend to need
 * collision-resistance beyond what 32 bits gives us for this scale.
 */
function randomHex(n: number): string {
  return crypto.randomUUID().replace(/-/g, "").slice(0, n);
}
