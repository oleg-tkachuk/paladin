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

// Security headers applied to every response. CSP starts strict
// (default-src 'self') and only opens what the app needs: Next.js
// injects inline styles and (in dev) inline/eval scripts, and the BFF
// talks to same-origin /api/rpc so connect-src 'self' suffices. The
// 'unsafe-inline'/'unsafe-eval' script allowances are dev-only; the
// production policy drops 'unsafe-eval'. Tighten further (nonces) once
// any remaining inline scripts are audited.
const isDev = process.env.NODE_ENV !== "production";
const csp = [
  "default-src 'self'",
  "base-uri 'self'",
  "frame-ancestors 'none'",
  "object-src 'none'",
  "img-src 'self' data: blob:",
  "font-src 'self' data:",
  "style-src 'self' 'unsafe-inline'",
  `script-src 'self' 'unsafe-inline'${isDev ? " 'unsafe-eval'" : ""}`,
  "connect-src 'self'",
  "form-action 'self'",
]
  .join("; ")
  .concat(isDev ? "" : "; upgrade-insecure-requests");

const securityHeaders = [
  { key: "Content-Security-Policy", value: csp },
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
    return [{ source: "/:path*", headers: securityHeaders }];
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
