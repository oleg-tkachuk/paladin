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
      // react-hooks v6 promoted these to error. `immutability` and
      // `preserve-manual-memoization` had a handful of real hits (forward
      // references in objects/page.tsx + a memo-dep mismatch in
      // useObjectKeys.ts); those are fixed, so the overrides are removed and
      // the rules now enforce at error (inherited from eslint-config-next).
      //
      // `set-state-in-effect` stays warn: its ~40 hits are the legitimate
      // fetch-on-mount / init-from-browser pattern in our data hooks, whose
      // proper resolution is an architectural data-fetching change (e.g.
      // TanStack Query), NOT a lint suppression. Re-promotion is gated on
      // that work — tracked in BACKLOG.md ("react-hooks v6: set-state-in-
      // effect re-promotion").
      "react-hooks/set-state-in-effect": "warn",
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
