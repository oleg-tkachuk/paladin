import type { NextConfig } from "next";

import { yamlConfig } from "./src/config";

const nextConfig: NextConfig = {
  /* config options here */
  output: "standalone",
  env: {
    PALADIN_GRPC_URL: process.env.PALADIN_GRPC_URL || yamlConfig.runtimeConfig.public.objectControlPlane.upstreamUrl,
  },
};

export default nextConfig;
