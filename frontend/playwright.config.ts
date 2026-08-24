/**
 * Playwright config for the Paladin E2E suite.
 *
 * Design rationale: specs/001-frontend-playwright-e2e/contracts/
 *   fixtures-api.md §"playwright.config.ts (root config)" and
 *   research.md R-005 (Chromium-only) + R-007 (parallelism +
 *   flake guard).
 *
 * Key choices:
 *   - Chromium only — internal admin tool; cross-engine adds
 *     little, triples runtime, triples the flake surface
 *     (R-005).
 *   - 0 retries locally — surface flake instead of masking
 *     it. 1 retry on CI (when `CI` env is set) only as
 *     forward-compat for the BACKLOG'd CI integration; v1
 *     ships without CI (FR-006).
 *   - webServer brings up the docker-compose test stack
 *     before tests; `reuseExistingServer` for local-iteration
 *     speed.
 *   - Strict locator + navigation timeouts catch silent
 *     regressions before they snowball into 30 s of "loading".
 *   - Failure artifacts (screenshot, video, trace) retained
 *     locally per FR-006 — CI upload deferred.
 */
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e",
  // Exclude fixtures from test discovery (they don't contain
  // describe/test blocks). Without this Playwright warns about
  // empty files.
  testIgnore: ["**/fixtures/**"],

  // An external stack is slower than the local compose one by construction:
  // every seeding call crosses an ingress, and objects land in a real object
  // store rather than a MinIO container on the same bridge. Against a
  // cluster the heaviest tests measure 28-48s — a multipart upload of 12 MiB
  // through presigned URLs is most of that — so the 30s default fails them
  // for being slow rather than wrong, which is the least useful kind of red.
  //
  // The rest of this config is already parameterised for an external target
  // (PALADIN_E2E_BASE_URL); the budget was the piece that was not.
  timeout:
    Number(process.env.PALADIN_E2E_TIMEOUT ?? 0) ||
    (process.env.PALADIN_E2E_BASE_URL ? 90_000 : 30_000),

  // Local: no retry — flake is surfaced. CI: 1 retry only when
  // --ci flag / CI env is set (forward-compat for deferred CI
  // integration). See FR-006.
  retries: process.env.CI ? 1 : 0,

  // Workers tuned so 6 tests fit the 3-minute budget. Default
  // (~half CPU count) on dev machines = 4 workers (R-007).
  workers: process.env.CI ? 2 : undefined,

  reporter: [
    ["list"],
    [
      "html",
      {
        outputFolder: "tests/e2e/test-results/html-report",
        open: "never",
      },
    ],
  ],

  use: {
    baseURL: process.env.PALADIN_E2E_BASE_URL ?? "http://localhost:3000",
    // Failure artifacts per FR-006. Local-only — no upload hook
    // wired in v1.
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    trace: "retain-on-failure",
    // Strict locator timeouts catch silent regressions. Scaled for an
    // external target for the same reason as the test budget above — a
    // navigation through an ingress is not a regression.
    actionTimeout: process.env.PALADIN_E2E_BASE_URL ? 20_000 : 10_000,
    navigationTimeout: process.env.PALADIN_E2E_BASE_URL ? 30_000 : 15_000,
  },

  // Chromium only per Clarification Q1.
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],

  // Bring up the test stack before any test runs. Reuses an
  // already-running stack for faster local iteration via
  // `pnpm run test:e2e:stack` in a separate terminal.
  // Targeting an external stack (a cluster behind port-forwards) means there
  // is nothing to bring up locally — starting compose would shadow it.
  webServer: process.env.PALADIN_E2E_BASE_URL
    ? undefined
    : {
        command:
          "docker compose -p paladin-e2e -f tests/e2e/docker-compose.test.yaml up --wait",
        url: "http://localhost:3000",
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
        stdout: "pipe",
        stderr: "pipe",
      },
});
