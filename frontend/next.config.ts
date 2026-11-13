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

// The production build is a static export, served by the console's Go server
// (server/, ADR-0049), which also sets the security headers and the CSP.
// `next dev` runs without it: with output "export" set, dev answers 404 for
// any value of a dynamic segment but the placeholder the export is built for.
const isProductionBuild = process.env.NODE_ENV === "production";

const nextConfig: NextConfig = {
  output: isProductionBuild ? "export" : undefined,
  env: {
    // PALADIN_GRPC_URL used to be set here from the config file's
    // paladin.upstreamUrl. Nothing read it — in either repo — while the BFF
    // reached its upstreams through PALADIN_DATA_URL / _IAM_ / _ADMIN_, which
    // the chart sets directly.
    //
    // NEXT_PUBLIC_* prefix is required for client-side access — without
    // it Next.js strips the var from the browser bundle and the
    // values render as undefined at runtime.
    NEXT_PUBLIC_UI_VERSION: uiMeta.version,
    NEXT_PUBLIC_UI_COMMIT: uiMeta.gitSha,
    NEXT_PUBLIC_UI_BUILD_TIME: uiMeta.buildTime,
  },
};

export default nextConfig;
