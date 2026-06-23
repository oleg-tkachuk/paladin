import { defineConfig } from "vitest/config";

// Unit tests run in node (pure logic: formatters, validators, the BFF
// audience gate). Component/DOM tests would add jsdom + RTL later; the
// first wave deliberately covers framework-free units so it stays fast
// and dependency-light. `@/` path aliases resolve via Vite's native
// tsconfig-paths support (vitest 4 / vite 6+).
export default defineConfig({
  resolve: { tsconfigPaths: true },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
    globals: false,
  },
});
