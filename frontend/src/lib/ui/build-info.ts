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

/** What a build made without release metadata calls its version. */
export const LOCAL_BUILD_VERSION = "local";

/** How much of a git SHA the console shows: git's own default abbreviation. */
export const SHORT_COMMIT_LENGTH = 7;

/** The abbreviated form of a commit SHA; empty stays empty. */
export function shortCommit(sha: string): string {
  return sha.trim().slice(0, SHORT_COMMIT_LENGTH);
}

/**
 * A release as one label: `v11.9.0 (a1b2c3d)`.
 *
 * Release builds carry the bare semver (the tag without its `v`), so the `v`
 * is added here, once. An unversioned build reads as "local", and an unknown
 * commit drops the parenthesised hash rather than showing empty brackets.
 */
export function formatBuildLabel(version: string, commit: string): string {
  const v = version.trim();
  const shown = !v ? LOCAL_BUILD_VERSION : /^\d/.test(v) ? `v${v}` : v;
  const sha = shortCommit(commit);
  return sha ? `${shown} (${sha})` : shown;
}

/**
 * True when the console and the backend were built from different commits.
 * Unknown on either side is not a skew: a local build has no commit to
 * compare.
 */
export function isBuildSkew(backendCommit: string, uiCommit: string): boolean {
  const a = shortCommit(backendCommit);
  const b = shortCommit(uiCommit);
  return a !== "" && b !== "" && a !== b;
}

// uiBuildInfo returns the UI bundle's own version metadata.
// `process.env.*` reads here resolve at compile time when the var is
// NEXT_PUBLIC_-prefixed; the result is a constant in the bundle.
export function uiBuildInfo(): BuildInfo {
  return {
    version: process.env.NEXT_PUBLIC_UI_VERSION || LOCAL_BUILD_VERSION,
    commit: shortCommit(process.env.NEXT_PUBLIC_UI_COMMIT || ""),
    buildTime: process.env.NEXT_PUBLIC_UI_BUILD_TIME || "",
  };
}
