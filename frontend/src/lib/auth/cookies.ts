// Edge-safe cookie name derivation. Imported by both the BFF (Node runtime)
// and the middleware (Edge runtime), so it must NOT pull in `next/headers`
// or anything Node-specific.

import type { Audience } from "@/constants";

const COOKIE_PREFIX = "paladin_rt_";

/** Cookie name for the refresh-token chain pinned to a given audience. */
export function refreshCookieName(audience: Audience): string {
  return `${COOKIE_PREFIX}${audience.replace(/^paladin-/, "")}`;
}
