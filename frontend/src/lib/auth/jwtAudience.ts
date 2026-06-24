import { z } from "zod";

import { AUDIENCES, type Plane } from "@/constants";
import { safeParseJson } from "@/lib/parseJson";

// `aud` per RFC 7519 is a string or string[]. Anything else (a hostile token
// claiming `aud: { $ne: null }`, a number, …) fails validation → null →
// treated as "no audience match", which fails the BFF forward closed.
const JwtAudPayloadSchema = z.object({
  aud: z.union([z.string(), z.array(z.string())]).optional(),
});

/**
 * Decodes a JWT's `aud` claim WITHOUT verifying the signature. The
 * cryptographic check is the backend's job (it holds the signing key);
 * the BFF only uses this to refuse forwarding a token whose audience
 * doesn't match the plane it's aimed at, so a `data` token can never be
 * tunnelled to the `admin` plane through the open proxy.
 *
 * Returns the set of audiences in the token, or null if the token is
 * malformed / has no `aud`. `aud` may be a string or string[] per RFC 7519.
 */
function audiencesOf(bearer: string): Set<string> | null {
  const raw = bearer.startsWith("Bearer ") ? bearer.slice(7) : bearer;
  const parts = raw.split(".");
  if (parts.length !== 3) return null;
  try {
    // base64url → JSON. atob handles standard base64; normalise url-safe
    // chars and padding first.
    const b64 = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const padded = b64 + "=".repeat((4 - (b64.length % 4)) % 4);
    const payload = safeParseJson(
      JwtAudPayloadSchema,
      typeof atob === "function"
        ? atob(padded)
        : Buffer.from(padded, "base64").toString("utf8"),
    );
    if (payload?.aud == null) return null;
    return new Set(Array.isArray(payload.aud) ? payload.aud : [payload.aud]);
  } catch {
    return null;
  }
}

/**
 * BFF audience gate. Returns true if it's safe to forward `bearer` to
 * `plane`. A present token MUST carry the plane's expected audience; an
 * absent token is allowed through (anonymous flows like AuthService.Login
 * on the iam plane — the backend's own interceptors decide from there).
 */
export function tokenMatchesPlane(
  bearer: string | null,
  plane: Plane,
): boolean {
  if (!bearer) return true; // anonymous — backend enforces
  const auds = audiencesOf(bearer);
  if (!auds) return false; // malformed token → refuse
  return auds.has(AUDIENCES[plane]);
}
