// The console's own version, commit and build time, read once as the build
// runs and baked into the bundle as NEXT_PUBLIC_UI_* (next.config.ts). The
// static export has no server process of its own to read them later:
// deploy/Dockerfile passes them as build args.

// What a build that was given no metadata reports: nothing. The readers show
// "local" for an empty version and hide an empty commit, which is true; a
// made-up number is not.
export const UNSET_META = "";

// What a build arg carries when no value was threaded through:
// deploy/Dockerfile defaults each to "undefined".
const PLACEHOLDERS = new Set(["undefined", "none", "unknown"]);

/** The first of values that is set and is no placeholder, else fallback. */
export function resolveMeta(
  values: readonly (string | undefined)[],
  fallback: string,
): string {
  for (const raw of values) {
    const value = (raw ?? "").trim();
    if (value && !PLACEHOLDERS.has(value)) return value;
  }
  return fallback;
}

/** The build metadata the bundle carries. */
export interface BuildMeta {
  version: string;
  gitSha: string;
  buildTime: string;
}

/**
 * The build's metadata from env, its VERSION file's contents when it has one
 * (versionFile), and the time the build runs (now).
 */
export function buildMeta(
  env: Readonly<Record<string, string | undefined>>,
  versionFile: string | undefined,
  now: Date,
): BuildMeta {
  return {
    version: resolveMeta([env.APP_VERSION, versionFile], UNSET_META),
    gitSha: resolveMeta([env.GIT_COMMIT_HASH], UNSET_META),
    buildTime: resolveMeta([env.BUILD_TIME], now.toISOString()),
  };
}
