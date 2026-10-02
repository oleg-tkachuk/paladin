import { readFileSync } from "node:fs";

// The shared secret gating /system/health.json on the backends — mirrors
// config.Runtime.HealthSnapshotToken. In production the chart mounts it from
// a Secret as a file and names the file here, so the token is never in the
// pod's environment. The plain variable stays for dev, and when neither is
// set the backends leave the endpoint open and no header is sent.
export const HEALTH_TOKEN_FILE_ENV = "PALADIN_HEALTH_SNAPSHOT_TOKEN_FILE";
export const HEALTH_TOKEN_ENV = "PALADIN_HEALTH_SNAPSHOT_TOKEN";

/**
 * The token to forward, or "" for none. A named file that cannot be read is
 * an error, not an empty token: forwarding nothing would turn a mounting
 * mistake into a health page of 401s that says nothing about why.
 */
export function resolveHealthToken(
  env: Record<string, string | undefined>,
  read: (path: string) => string = (p) => readFileSync(p, "utf8"),
): string {
  const file = env[HEALTH_TOKEN_FILE_ENV];
  if (file) {
    try {
      return read(file).trim();
    } catch (err) {
      const why = err instanceof Error ? err.message : String(err);
      throw new Error(
        `${HEALTH_TOKEN_FILE_ENV}=${file} cannot be read: ${why}`,
      );
    }
  }
  return env[HEALTH_TOKEN_ENV] ?? "";
}
