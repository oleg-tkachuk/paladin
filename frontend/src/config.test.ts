import { describe, expect, it } from "vitest";

import { UNSET_META, configSchema, loadConfig, resolveMeta } from "./config";

// This schema used to describe four subtrees; three of them were read by no
// code. `paladin.upstreamUrl` fed an env var nothing consumed, `oidc.*` was for a
// browser-side OIDC flow that never shipped, and `auth.devToken` was declared
// here while its only reader greps the YAML directly in a shell script. What is
// left is uiMetadata, and the tests below pin the two properties that removal
// depends on: an empty document has to parse, and the removed keys have to be
// tolerated rather than rejected.

describe("configSchema", () => {
  it("parses an empty document", () => {
    // The chart renders `config: {}` into the ConfigMap, so this is the shape
    // the container actually reads. Zod 4's `.default()` takes the output type,
    // so the tree is seeded with `.prefault({})`; get that wrong and every pod
    // logs a validation error on boot and runs on defaults anyway.
    const parsed = configSchema.parse({});
    expect(parsed.runtimeConfig.public.uiMetadata.service).toBe(
      "paladin-console",
    );
    expect(parsed.runtimeConfig.public.configPath).toBe("unknown");
  });

  it("tolerates the removed subtrees and drops them", () => {
    // Not `.strict()`, on purpose: a values overlay outside this repo still
    // sends these. Accepting and ignoring them is what keeps that overlay from
    // failing a build over config this code no longer reads.
    const parsed = configSchema.parse({
      runtimeConfig: {
        public: {
          paladin: { baseUrl: "/api/paladin", upstreamUrl: "http://x:8080" },
          oidc: { authority: "https://idp.example", disableAuth: true },
          auth: { devToken: "a.b.c" },
        },
      },
    });
    const pub = parsed.runtimeConfig.public as Record<string, unknown>;
    expect(Object.keys(pub).sort()).toEqual(["configPath", "uiMetadata"]);
  });

  it("lets the file override the build-arg metadata", () => {
    const parsed = configSchema.parse({
      runtimeConfig: {
        public: {
          uiMetadata: { version: "9.9.9" },
          configPath: "/app/configs/config.yaml",
        },
      },
    });
    expect(parsed.runtimeConfig.public.uiMetadata.version).toBe("9.9.9");
    expect(parsed.runtimeConfig.public.configPath).toBe(
      "/app/configs/config.yaml",
    );
  });

  it("still rejects a value of the wrong type", () => {
    // Without this the three tests above would also pass against a schema that
    // validates nothing at all.
    const bad = configSchema.safeParse({
      runtimeConfig: { public: { uiMetadata: { version: 9.9 } } },
    });
    expect(bad.success).toBe(false);
  });
});

describe("loadConfig", () => {
  it("reads the dev config this repo ships", () => {
    // Path 2 of the loader is `configs/config.yaml`, relative to the working
    // directory — which is the package root under vitest, so this exercises the
    // real file rather than a fixture of it.
    const cfg = loadConfig();
    expect(cfg.runtimeConfig.public.configPath).toBe("configs/config.yaml");
    expect(cfg.runtimeConfig.public.uiMetadata.service).toBe("paladin-console");
  });
});

describe("uiMetadata without build args", () => {
  // `docker build` without APP_VERSION used to report "1.4.0" — a version the
  // console never had — beside the backend's real one on the dashboard.
  it("reports no version rather than inventing one", () => {
    const meta = configSchema.parse({}).runtimeConfig.public.uiMetadata;
    expect(meta.version).toBe(UNSET_META);
    expect(meta.gitSha).toBe(UNSET_META);
  });

  it.each([undefined, "", "undefined", "none", "unknown", "  "])(
    "treats %j as unset",
    (placeholder) => {
      expect(resolveMeta(placeholder, undefined, undefined, UNSET_META)).toBe(
        UNSET_META,
      );
    },
  );

  it("takes the first real value in order", () => {
    expect(resolveMeta(undefined, "2.0.0", "3.0.0", UNSET_META)).toBe("2.0.0");
    expect(resolveMeta("1.0.0", "2.0.0", undefined, UNSET_META)).toBe("1.0.0");
  });
});
