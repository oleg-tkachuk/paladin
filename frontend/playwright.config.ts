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

// The compose file publishes the console on PALADIN_E2E_PORT_UI (default 3000)
// so a second stack can run beside this one. Both the browser's baseURL and the
// webServer readiness probe have to follow it, or the suite waits on a port it
// does not own and drives one it did not start.
const UI_URL = `http://localhost:${process.env.PALADIN_E2E_PORT_UI ?? "3000"}`;

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
    // PALADIN_E2E_BASE_URL means "an external stack is already running" — it is
    // what switches the webServer block off below. So it must NOT be set merely
    // to follow a port override; the default follows PALADIN_E2E_PORT_UI
    // instead, and the two stay in step.
    baseURL: process.env.PALADIN_E2E_BASE_URL ?? UI_URL,
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
    // A deployed target serves a certificate from the environment's own CA.
    // Whether the browser accepts it depends on the machine's trust store —
    // it passes where that CA happens to be installed and fails everywhere
    // else, which is a bad thing for a suite to depend on. Scoped to external
    // runs: the compose stack is plain HTTP and unaffected either way.
    ignoreHTTPSErrors: Boolean(process.env.PALADIN_E2E_BASE_URL),
  },

  // Chromium only per Clarification Q1.
  projects: [
    // Runs before anything else and gates the rest. Its job is to turn "the
    // environment is full of leftovers from aborted runs" — which the suite
    // otherwise reports as three unrelated product-looking failures deep into
    // the run — into one named failure at second zero. A project rather than
    // globalSetup so it runs after webServer has the stack up, and so its
    // failure is a red test with a message rather than a stack trace before
    // the reporter starts.
    {
      name: "environment",
      testMatch: /environment\.setup\.ts$/,
    },
    {
      name: "chromium",
      dependencies: ["environment"],
      // Without this the guard would also run again inside the browser
      // project, since a project with no testMatch takes every file in
      // testDir.
      testIgnore: /environment\.setup\.ts$/,
      use: {
        ...devices["Desktop Chrome"],
        // Desktop Chrome defaults to 1280x720, and several console forms are
        // taller than that — the budget and bucket-settings submit buttons sit
        // below the fold at y≈748. Playwright scrolls to reach them, but on a
        // page that is still settling the scroll changes the layout under it,
        // so the click lands on a moving target and times out. The button was
        // never moving on its own: elementFromPoint at its centre returned
        // null because the point was outside the viewport entirely.
        //
        // A taller viewport puts those controls on screen and removes the
        // scroll from the equation. 900 is the shortest common laptop height,
        // so it also matches what an operator actually sees.
        viewport: { width: 1440, height: 900 },
      },
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
        url: UI_URL,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
        stdout: "pipe",
        stderr: "pipe",
      },
});
