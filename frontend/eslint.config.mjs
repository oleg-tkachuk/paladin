import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  {
    rules: {
      "@typescript-eslint/no-unused-vars": [
        "warn",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
        },
      ],
      // react-hooks v6 rules (`immutability`, `preserve-manual-memoization`,
      // `set-state-in-effect`) all enforce at error, inherited from
      // eslint-config-next — no overrides. The set-state-in-effect override
      // is gone: the fetch-on-mount / init-from-browser pattern was migrated
      // to TanStack Query (data fetching) + render-phase adjust-on-change
      // (form seeding). The only two remaining set-state-in-effect sites are
      // genuine post-hydration browser reads (URL hash in ObjectDetailView,
      // localStorage in the objects page) and carry inline eslint-disable
      // comments explaining why an effect is correct there.
    },
  },
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    ".agents/**",
  ]),
]);

export default eslintConfig;
