/**
 * UI-driven authentication helpers.
 *
 * Spec.md FR-003 mandates that tests drive the browser through
 * real UI surfaces — login goes through the actual /login form,
 * not an API shortcut. Reasoning: the AuthGate + JWT + Cedar
 * chain is exactly the surface most likely to regress silently,
 * and bypassing it in tests would defeat the regression-guard
 * promise.
 *
 * `loginAsAdmin` is called from EVERY user-story test's
 * beforeEach EXCEPT US1 (auth.spec.ts), which drives the form
 * directly to assert on the AuthGate redirect contract.
 */
import type { Page } from "@playwright/test";
import { expect } from "@playwright/test";
import { SEEDED_ADMIN } from "./credentials";

/**
 * Drive the browser through the real /login form using the
 * seeded admin credentials. The form selector targets are the
 * canonical IDs from frontend/src/app/login/page.tsx
 * (id="subject" and id="password"). Asserts the post-login URL
 * is NOT /login — if the form silently fails, this throws here
 * with a clear message rather than later with a mysterious
 * "list page empty" error.
 *
 * Side effect: an authenticated session cookie + localStorage
 * tokens land in `page.context()`. Subsequent navigations
 * remain authenticated for the lifetime of that context.
 */
export async function loginAsAdmin(page: Page): Promise<void> {
  await page.goto("/login");
  await page.fill("#subject", SEEDED_ADMIN.subject);
  await page.fill("#password", SEEDED_ADMIN.password);
  await page.getByRole("button", { name: "Sign in" }).click();
  // The form bounces to either `next=` (if present) or a
  // default landing page. Either way, we MUST leave /login —
  // if we don't, the credentials were rejected.
  await expect(page).not.toHaveURL(/\/login(\?.*)?$/, {
    timeout: 10_000,
  });
}

/**
 * Sign out via the topbar menu. Used by US1 to reset between
 * acceptance scenarios within the same test; most tests are
 * atomic enough that they don't need this — the per-test
 * browser context is torn down automatically by Playwright.
 *
 * The exact selector depends on the topbar implementation. If
 * the topbar restructures, update the locator chain here; do
 * NOT bypass to a direct /api/auth/logout call without
 * justifying the FR-003 deviation.
 */
export async function logout(page: Page): Promise<void> {
  // The user-menu trigger is in the topbar; the logout item
  // sits inside its dropdown. We use `getByRole` + text to stay
  // resilient to layout shuffles.
  await page.getByRole("button", { name: /user menu|account/i }).click();
  await page.getByRole("menuitem", { name: /sign out|logout/i }).click();
  await expect(page).toHaveURL(/\/login/, { timeout: 5_000 });
}
