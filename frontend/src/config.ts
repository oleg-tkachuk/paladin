import fs from "node:fs";
import yaml from "js-yaml";
import { z } from "zod";

// Resolve UI metadata with robust fallbacks
const envFileVars: Record<string, string> = {};

// Helper to prioritize non-placeholder strings
const resolveMeta = (
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
  "1.4.0",
);
const gitSha = resolveMeta(
  process.env.GIT_COMMIT_HASH,
  envFileVars.GIT_COMMIT_HASH,
  process.env.NEXT_PUBLIC_UI_METADATA_GIT_SHA,
  "local",
);
const buildTime = resolveMeta(
  process.env.BUILD_TIME,
  envFileVars.BUILD_TIME,
  process.env.NEXT_PUBLIC_UI_METADATA_BUILD_TIME,
  new Date().toISOString(),
);

const configSchema = z.object({
  runtimeConfig: z.object({
    public: z.object({
      objectControlPlane: z
        .object({
          baseUrl: z.string().default("/api/paladin"),
          upstreamUrl: z.string().default("http://localhost:8082"),
          timeoutMs: z.number().default(5000),
        })
        .default({
          baseUrl: "/api/paladin",
          upstreamUrl: "http://localhost:8082",
          timeoutMs: 5000,
        }),
      oidc: z
        .object({
          authority: z.string().default(""),
          clientId: z.string().default(""),
          redirectUri: z.string().default(""),
          postLogoutRedirectUri: z.string().default(""),
          scope: z.string().default("openid profile email"),
          disableAuth: z.boolean().default(false),
        })
        .default({
          authority: "",
          clientId: "",
          redirectUri: "",
          postLogoutRedirectUri: "",
          scope: "openid profile email",
          disableAuth: false,
        }),
      auth: z
        .object({
          devToken: z.string().default(""),
        })
        .default({ devToken: "" }),
      uiMetadata: z
        .object({
          service: z.string().default("paladin-ui"),
          version: z.string().default(appVersion),
          gitSha: z.string().default(gitSha),
          buildTime: z.string().default(buildTime),
        })
        .default({
          service: "paladin-ui",
          version: appVersion,
          gitSha: gitSha,
          buildTime: buildTime,
        }),
      configPath: z.string().default("unknown"),
    }),
  }),
});

export const loadConfig = () => {
  // Priority:
  // 1. /app/config/config.yaml (K8s ConfigMap volume)
  // 2. /app/configs/config.yaml (Standard container path)
  // 3. configs/paladin-ui.yaml (Local dev)
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
          console.error(
            `[Config] Validation failed for ${path}:`,
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
