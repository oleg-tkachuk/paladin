import type { NextConfig } from "next";

import { yamlConfig } from "./src/config";

// Bake the UI's own version + commit + build-time into the client
// bundle. The dashboard renders these alongside the backend's
// /system/version output so operators can spot a UI / backend skew
// at a glance — e.g. backend redeployed to a new tag while a stale
// UI tab is still served from the browser cache. Backend version
// already comes through SystemService.GetVersion (used by useStats);
// the NEXT_PUBLIC_* vars below cover the UI half.
const uiMeta = yamlConfig.runtimeConfig.public.uiMetadata;

const nextConfig: NextConfig = {
  output: "standalone",
  env: {
    PALADIN_GRPC_URL:
      process.env.PALADIN_GRPC_URL ||
      yamlConfig.runtimeConfig.public.objectControlPlane.upstreamUrl,
    // NEXT_PUBLIC_* prefix is required for client-side access — without
    // it Next.js strips the var from the browser bundle and the
    // values render as undefined at runtime.
    NEXT_PUBLIC_UI_VERSION: uiMeta.version,
    NEXT_PUBLIC_UI_COMMIT: uiMeta.gitSha,
    NEXT_PUBLIC_UI_BUILD_TIME: uiMeta.buildTime,
  },
};

export default nextConfig;
