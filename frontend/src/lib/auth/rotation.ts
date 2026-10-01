import { decodeJwt } from "jose";

/**
 * How old the session's refresh token must be before /api/auth/me rotates it.
 *
 * /me used to rotate on every page load. Rotation is the one step whose
 * response cannot be lost: IAM supersedes the old token the moment it mints
 * the new one, and if the response carrying the new cookie never reaches the
 * browser — a navigation that starts while /me is in flight — the browser
 * keeps the old token. Within IAM's supersession grace (30s) that is
 * forgiven; after it, the old token is a replay, reuse detection revokes the
 * whole family, and the operator is signed out of a session nothing was wrong
 * with. Reproduced on the cluster by dropping one /me Set-Cookie.
 *
 * Rotating only once the token is this old keeps every quick navigation off
 * that path: a young token is read with ExchangeAudience, which consumes
 * nothing. Five minutes bounds how long a stolen refresh token can go before
 * the next rotation exposes it to reuse detection.
 */
export const ROTATE_AFTER_SECONDS = 5 * 60;

const MS_PER_SECOND = 1000;

/**
 * Whether the session's refresh token is due for rotation at `nowMs`.
 *
 * Reads `iat` without verifying the signature: this only schedules the
 * rotation, and IAM verifies the token on either path. A token whose age
 * cannot be read is rotated, which is what /me always did.
 */
export function rotationDue(refreshToken: string, nowMs: number): boolean {
  let issuedAt: number | undefined;
  try {
    issuedAt = decodeJwt(refreshToken).iat;
  } catch {
    return true;
  }
  if (issuedAt === undefined) return true;
  return nowMs / MS_PER_SECOND - issuedAt >= ROTATE_AFTER_SECONDS;
}
