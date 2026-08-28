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
 * Sign out through the sidebar control.
 *
 * This helper claimed to be "used by US1 to reset between acceptance
 * scenarios" and was used by nothing — no spec referenced it, and its locator
 * chain (a "user menu" button, then a menuitem) matched no element on the
 * page. So the console's logout had no end-to-end coverage at all, which is
 * how it spent its life revoking nothing on the server while its unit test,
 * mocking Revoke as succeeding, passed throughout.
 *
 * logout.spec.ts now covers it, and asserts the part that matters: the session
 * is dead SERVER-side afterwards, not merely absent from the browser.
 */
export async function logout(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login/, { timeout: 10_000 });
}
