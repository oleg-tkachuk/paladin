// UI build-info accessor — reads the NEXT_PUBLIC_* env vars
// next.config.ts bakes in at build time. Centralised here so the
// Dashboard, /health, footer chips, and any future surface read
// the same source of truth.
//
// Backend build-info lives separately, plumbed via
// SystemService.GetVersion → useStats() → stats.version. Operators
// often need both side-by-side to spot a UI ↔ backend version
// skew (browser cache serving a stale bundle, helm rollback that
// only flipped one half, etc.) — the Dashboard renders both rows.

export type BuildInfo = {
  /** Semver string. "local" when the binary was built without
   *  -ldflags / NEXT_PUBLIC_UI_VERSION. */
  version: string;
  /** Short git SHA (first 7 chars). Empty when unknown. */
  commit: string;
  /** RFC3339 build timestamp; empty when unknown. */
  buildTime: string;
};

// uiBuildInfo returns the UI bundle's own version metadata.
// `process.env.*` reads here resolve at compile time when the var is
// NEXT_PUBLIC_-prefixed; the result is a constant in the bundle.
export function uiBuildInfo(): BuildInfo {
  return {
    version: process.env.NEXT_PUBLIC_UI_VERSION || "local",
    commit: (process.env.NEXT_PUBLIC_UI_COMMIT || "").slice(0, 7),
    buildTime: process.env.NEXT_PUBLIC_UI_BUILD_TIME || "",
  };
}
