import type { NextConfig } from "next";

import { yamlConfig } from "./src/config";
import {
  OAUTH_CONSENT_PATH,
  contentSecurityPolicy,
} from "./src/lib/security/csp";

// Bake the UI's own version + commit + build-time into the client
// bundle. The dashboard renders these alongside the backend's
// /system/version output so operators can spot a UI / backend skew
// at a glance — e.g. backend redeployed to a new tag while a stale
// UI tab is still served from the browser cache. Backend version
// already comes through SystemService.GetVersion (used by useStats);
// the NEXT_PUBLIC_* vars below cover the UI half.
const uiMeta = yamlConfig.runtimeConfig.public.uiMetadata;

// Security headers applied to every response; the CSP is built in
// src/lib/security/csp.ts.
const isDev = process.env.NODE_ENV !== "production";

const securityHeaders = [
  { key: "Content-Security-Policy", value: contentSecurityPolicy({ isDev }) },
  // HSTS only in production. On http://localhost dev a 2-year HSTS would
  // make the browser refuse plain-HTTP to localhost for everything on
  // that port — poisoning every developer's machine after one visit.
  ...(isDev
    ? []
    : [
        {
          key: "Strict-Transport-Security",
          value: "max-age=63072000; includeSubDomains; preload",
        },
      ]),
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  { key: "X-DNS-Prefetch-Control", value: "off" },
  {
    key: "Permissions-Policy",
    value: "camera=(), microphone=(), geolocation=()",
  },
];

const nextConfig: NextConfig = {
  output: "standalone",
  async headers() {
    return [
      { source: "/:path*", headers: securityHeaders },
      // Later wins for the same header: the consent page's policy without
      // form-action (see contentSecurityPolicy).
      {
        source: OAUTH_CONSENT_PATH,
        headers: [
          {
            key: "Content-Security-Policy",
            value: contentSecurityPolicy({ isDev, consent: true }),
          },
        ],
      },
    ];
  },
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
