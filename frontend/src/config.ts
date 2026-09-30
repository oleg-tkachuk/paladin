import fs from "node:fs";
import yaml from "js-yaml";
import { z } from "zod";

// Resolve UI metadata with robust fallbacks
const envFileVars: Record<string, string> = {};

// What a build that was given no metadata reports: nothing. The readers show
// "local" for an empty version and hide an empty commit, which is true; a
// made-up number is not.
export const UNSET_META = "";

// Helper to prioritize non-placeholder strings
export const resolveMeta = (
  primary: string | undefined,
  secondary: string | undefined,
  tertiary: string | undefined,
  fallback: string,
) => {
  const val = (primary || secondary || tertiary || "").trim();
  if (!val || val === "undefined" || val === "none" || val === "unknown")
    return fallback;
  return val;
};

const appVersion = resolveMeta(
  process.env.APP_VERSION,
  envFileVars.APP_VERSION,
  fs.existsSync("VERSION")
    ? fs.readFileSync("VERSION", "utf8").trim()
    : undefined,
  UNSET_META,
);
const gitSha = resolveMeta(
  process.env.GIT_COMMIT_HASH,
  envFileVars.GIT_COMMIT_HASH,
  process.env.NEXT_PUBLIC_UI_METADATA_GIT_SHA,
  UNSET_META,
);
const buildTime = resolveMeta(
  process.env.BUILD_TIME,
  envFileVars.BUILD_TIME,
  process.env.NEXT_PUBLIC_UI_METADATA_BUILD_TIME,
  new Date().toISOString(),
);

// Only what something reads. The schema used to carry `paladin`, `oidc` and
// `auth` subtrees as well; nothing in the app ever read them. `oidc.authority`
// / `clientId` were for a browser-side OIDC flow that never landed (there is no
// OIDC client library in package.json, and the console authenticates through
// the backend's IAM plane), and `auth.devToken` was read by nothing while being
// shipped into a ConfigMap — a comment warned it reached the browser, which it
// could not, because no code fetched it.
//
// uiMetadata is the one live subtree, and even it is a fallback: the defaults
// below read APP_VERSION / GIT_COMMIT_HASH / BUILD_TIME, which deploy/Dockerfile
// threads in as build args, and next.config.ts bakes the result into
// NEXT_PUBLIC_UI_* at build time.
//
// Deliberately NOT `.strict()`: zod drops unknown keys, and that is what lets a
// values overlay still send the removed subtrees without failing the build. A
// strict schema here would reject a config this code no longer cares about.
export const configSchema = z.object({
  runtimeConfig: z
    .object({
      public: z
        .object({
          uiMetadata: z
            .object({
              service: z.string().default("paladin-console"),
              version: z.string().default(appVersion),
              gitSha: z.string().default(gitSha),
              buildTime: z.string().default(buildTime),
            })
            .default({
              service: "paladin-console",
              version: appVersion,
              gitSha: gitSha,
              buildTime: buildTime,
            }),
          configPath: z.string().default("unknown"),
        })
        .prefault({}),
      // `.prefault`, not `.default`: zod 4's default takes the OUTPUT type, so a
      // partial `{}` is a type error, while prefault seeds the INPUT and lets the
      // leaf defaults fill themselves in. Every level being optional is the point
      // — a ConfigMap rendered from an empty `config:` must parse, not warn.
    })
    .prefault({}),
});

export const loadConfig = () => {
  // Priority:
  // 1. /app/config/config.yaml (K8s ConfigMap volume)
  // 2. /app/configs/config.yaml (Standard container path)
  // 3. configs/paladin-console.yaml (Local dev)
  const CONFIG_PATHS = ["/app/configs/config.yaml", "configs/config.yaml"];

  for (const path of CONFIG_PATHS) {
    if (fs.existsSync(path)) {
      console.log(`[Config] Loading from: ${path}`);
      try {
        const fileContents = fs.readFileSync(path, "utf8");
        const parsed = yaml.load(fileContents) as Record<string, unknown>;

        // Inject configPath for verification
        if (parsed && typeof parsed === "object" && "runtimeConfig" in parsed) {
          const runtimeConfig = parsed.runtimeConfig as Record<string, unknown>;
          if (typeof runtimeConfig === "object" && "public" in runtimeConfig) {
            const pub = runtimeConfig.public as Record<string, unknown>;
            pub.configPath = path;
          }
        }

        const result = configSchema.safeParse(parsed);
        if (result.success) {
          return result.data;
        } else {
          // Logged, not thrown: every field in the schema is optional now and
          // its one live subtree falls back to the build-arg metadata, so a
          // rejected file costs the console nothing it was using. Say that,
          // rather than leaving a bare stack trace that reads like an outage.
          console.error(
            `[Config] ${path} does not match the expected shape; continuing on defaults:`,
            result.error,
          );
        }
      } catch (e) {
        console.error(`[Config] Failed to parse ${path}:`, e);
      }
    }
  }
  console.warn("[Config] No config file found, using defaults");
  return configSchema.parse({
    runtimeConfig: { public: { configPath: "default_values" } },
  });
};

export const yamlConfig = loadConfig();
