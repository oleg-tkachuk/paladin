import { defineConfig } from "vitest/config";

// Two projects, split by file extension:
//   - node  (*.test.ts):  pure logic — formatters, validators, the BFF
//     audience gate, hook reducers. Fast, no DOM.
//   - dom   (*.test.tsx): React component/page tests via @testing-library
//     under jsdom, with jest-dom matchers + auto-cleanup (src/test/setup.ts).
// `@/` path aliases resolve via Vite's native tsconfig-paths support
// (vitest 4 / vite 6+).
export default defineConfig({
  resolve: { tsconfigPaths: true },
  test: {
    globals: false,
    projects: [
      {
        extends: true,
        test: {
          name: "node",
          environment: "node",
          include: ["src/**/*.test.ts"],
        },
      },
      {
        extends: true,
        test: {
          name: "dom",
          environment: "jsdom",
          include: ["src/**/*.test.tsx"],
          setupFiles: ["./src/test/setup.ts"],
          // Node 25+ defines a global localStorage that is undefined unless
          // --localstorage-file is given, and jsdom's own does not replace a
          // global that already exists. Node 24 accepts this spelling too.
          execArgv: ["--no-experimental-webstorage"],
        },
      },
    ],
  },
});
