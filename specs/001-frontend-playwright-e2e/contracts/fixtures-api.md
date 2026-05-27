# Contract — Fixture Helper API

The fixture helpers under `frontend/tests/e2e/fixtures/` are
the **only** "API" this feature exposes. They're the contract
the test files depend on. Every signature below is stable for
the lifetime of v1 — changes require a new spec or a
clarification cycle.

## `fixtures/credentials.ts`

```typescript
// NEVER true in prod. e2e-only fixture credentials.
// Mirrors the local-overlay convention in gitops/.../overlays/local.
// Constitution Principle V requires the warning above to remain.
export const SEEDED_ADMIN = {
  subject: "e2e-admin@local",
  password: "e2e-not-a-secret-2026",
} as const;
```

No behaviour, just constants. Imported by `auth.ts` and any
test that asserts on the seeded admin's identity.

## `fixtures/unique.ts`

```typescript
/**
 * Return a UUID-suffixed identifier safe to use across
 * parallel tests without collision. 8 hex chars (~32 bits)
 * is enough entropy for ≤4 parallel workers — the per-test
 * tenant slug UNIQUE constraint (migration 033) catches the
 * extraordinary case if it ever fires.
 */
export function uniqueSlug(prefix: string): string;

/**
 * Same shape but for human-facing display names. Returns
 * `${prefix} ${randomHex(8)}`.
 */
export function uniqueDisplayName(prefix: string): string;
```

## `fixtures/auth.ts`

```typescript
import type { Page } from "@playwright/test";

/**
 * Drive the browser through the real /login form using the
 * seeded admin credentials. Used by EVERY test's beforeEach
 * (except US1, which exercises the redirect path manually).
 *
 * Asserts that the post-login URL is NOT /login — if the form
 * silently fails, the test fails here with a clear message
 * rather than later with a mysterious "tenant list empty"
 * error.
 *
 * Returns nothing — side effect is the authenticated session
 * cookie in `page.context()`.
 */
export async function loginAsAdmin(page: Page): Promise<void>;

/**
 * Sign out via the topbar menu. Used by US1 to reset between
 * acceptance scenarios within the same test (rare; usually
 * tests are atomic and `loginAsAdmin` in beforeEach is enough).
 */
export async function logout(page: Page): Promise<void>;
```

## `fixtures/seed.ts`

All seed helpers use the Connect-RPC clients
(`@connectrpc/connect`) against the test stack's API/admin
planes — NOT through the UI. This is the deliberate exception
to FR-003: seeding is setup, not verification.

```typescript
import type { APIRequestContext } from "@playwright/test";

interface SeededTenant {
  tenantId: string; // UUID
  slug: string;
  displayName: string;
  resourceVersion: bigint;
}

/**
 * Create a tenant via TenantService.CreateTenant. The slug and
 * displayName are UUID-suffixed automatically; pass overrides
 * only when a specific shape matters (e.g. US2 explicitly
 * names "acme" and "globex" prefixes).
 *
 * The platform-admin Bearer token is fetched via the IAM
 * plane's Login RPC and cached in module scope for the
 * lifetime of the test stack.
 */
export async function seedTenant(opts?: {
  slugPrefix?: string;
  displayNamePrefix?: string;
}): Promise<SeededTenant>;

interface SeededBucket {
  backendId: string; // chart-config default backend
  bucketName: string;
  displayName: string;
}

/**
 * Create a bucket under the given tenant via
 * BucketService.CreateBucket. Returns the resource-name parts
 * the UI uses for routing.
 */
export async function seedBucket(opts: {
  tenantId: string;
  bucketNamePrefix?: string;
}): Promise<SeededBucket>;

interface SeededObjectKey {
  tenantId: string;
  bucketBackendId: string;
  bucketName: string;
  objectKey: string;
}

/**
 * Create an ObjectKey under the given (tenant, bucket).
 * Generated key is `e2e/${randomHex(8)}` so the key column's
 * UNIQUE constraint is satisfied.
 */
export async function seedObjectKey(opts: {
  tenantId: string;
  bucketBackendId: string;
  bucketName: string;
}): Promise<SeededObjectKey>;
```

## `playwright.config.ts` (root config)

```typescript
import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e",
  // Exclude fixtures from test discovery (they don't contain
  // describe/test blocks). Without this Playwright will warn
  // about empty files.
  testIgnore: ["**/fixtures/**"],

  // Local: no retry — flake is surfaced. CI: 1 retry only
  // when --ci flag is set (deferred FR-006 path).
  retries: process.env.CI ? 1 : 0,

  // Workers tuned so 6 tests fit the 3-minute budget. Default
  // (~half CPU count) on dev machines = 4 workers — checks
  // out per R-007.
  workers: process.env.CI ? 2 : undefined,

  reporter: [
    ["list"],
    ["html", { outputFolder: "tests/e2e/test-results/html-report", open: "never" }],
  ],

  use: {
    baseURL: "http://localhost:3000",
    // Failure artifacts per FR-006. Local-only — no upload
    // hook wired in v1.
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    trace: "retain-on-failure",
    // Strict locator timeouts catch silent regressions.
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
  },

  // Chromium only per Clarification Q1.
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],

  // Bring up the test stack before any test runs. Reuses if
  // already up (faster local iteration via `docker compose up
  // -d` in a separate terminal).
  webServer: {
    command:
      "docker compose -p paladin-e2e -f tests/e2e/docker-compose.test.yaml up --wait",
    url: "http://localhost:3000",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
```

## `package.json` scripts (additions)

```jsonc
{
  "scripts": {
    "test:e2e": "playwright test",
    "test:e2e:headed": "playwright test --headed",
    "test:e2e:debug": "playwright test --debug",
    "test:e2e:stack": "docker compose -p paladin-e2e -f tests/e2e/docker-compose.test.yaml up",
    "test:e2e:stack:down": "docker compose -p paladin-e2e -f tests/e2e/docker-compose.test.yaml down -v"
  }
}
```
