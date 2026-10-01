/**
 * US1 — Login + AuthGate redirect.
 *
 * Spec: specs/001-frontend-playwright-e2e/spec.md §"User Story 1".
 * The highest-risk surface in the application: a regression here
 * either silently leaks unauthenticated traffic into authed pages
 * OR breaks the bounce-back loop for legitimate users.
 *
 * Four scenarios, each independently testable:
 *   - deep-link redirect → /login?next=<path>
 *   - post-login bounce back to ?next= target
 *   - default landing on no `next` param (loop prevention)
 *   - invalid credentials stay on /login with error visible
 *   - a cross-origin POST to /api is refused by the proxy (CSRF)
 *
 * No `beforeEach` here — every scenario opens a fresh
 * BrowserContext so cookies / localStorage from one scenario
 * can't pollute the next.
 */
import { test, expect } from "@playwright/test";
import { SEEDED_ADMIN } from "./fixtures/credentials";

// Any state-changing /api route will do; logout is reachable without a
// session, so the proxy's verdict is the only thing that can refuse it.
const UNSAFE_API_ROUTE = "/api/auth/logout";
// RFC 2606 reserves .invalid, so this can never be the console's own host.
const FOREIGN_ORIGIN = "https://csrf.invalid";
const CSRF_STATUS = 403;
const CSRF_MESSAGE = "cross-origin request rejected";

test.describe("US1 — Login + AuthGate", () => {
  test("deep-link redirect: navigating to /tenants without a session bounces to /login?next=%2Ftenants", async ({
    browser,
  }) => {
    // Fresh context = no cookies, no localStorage — exactly the
    // shape an operator hitting a deep link in a new tab sees.
    const context = await browser.newContext();
    const page = await context.newPage();
    try {
      await page.goto("/tenants");
      // AuthGate must rewrite the URL with the encoded `next`
      // pointing back at /tenants. We assert on the EXACT
      // encoded form to catch any regression that drops or
      // double-encodes the segment.
      await expect(page).toHaveURL(/\/login\?next=%2Ftenants$/, {
        timeout: 10_000,
      });
      // And the login form is actually rendered (not e.g. a
      // blank page while AuthGate is still resolving).
      await expect(page.locator("#subject")).toBeVisible();
      await expect(page.locator("#password")).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test("post-login bounces back to ?next= target", async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    try {
      await page.goto("/login?next=/tenants");
      await page.fill("#subject", SEEDED_ADMIN.subject);
      await page.fill("#password", SEEDED_ADMIN.password);
      await page.getByRole("button", { name: "Sign in" }).click();
      // Must land on /tenants — NOT on /, NOT on /login.
      await expect(page).toHaveURL(/\/tenants(\?.*)?$/, {
        timeout: 10_000,
      });
      // Page chrome rendered — proves we got past AuthGate, not
      // stuck on a loading splash.
      await expect(page.locator("main")).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test("default landing on no `next` param (loop prevention)", async ({
    browser,
  }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    try {
      await page.goto("/login");
      await page.fill("#subject", SEEDED_ADMIN.subject);
      await page.fill("#password", SEEDED_ADMIN.password);
      await page.getByRole("button", { name: "Sign in" }).click();
      // Per frontend/src/app/login/page.tsx:24, default is `/`.
      // Critical: we MUST NOT land back on /login — that's the
      // bounce-loop regression the spec calls out.
      await expect(page).not.toHaveURL(/\/login(\?.*)?$/, {
        timeout: 10_000,
      });
    } finally {
      await context.close();
    }
  });

  test("invalid credentials surface an error and keep the form on /login", async ({
    browser,
  }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    try {
      await page.goto("/login");
      await page.fill("#subject", SEEDED_ADMIN.subject);
      await page.fill("#password", "definitely-not-the-real-password");
      await page.getByRole("button", { name: "Sign in" }).click();
      // URL must still be /login (no bounce on failure).
      await expect(page).toHaveURL(/\/login(\?.*)?$/, { timeout: 5_000 });
      // Some kind of error message appears — exact wording can
      // shift over time; we match the destructive-styled box
      // login/page.tsx renders. If this selector starts being
      // brittle, swap to a getByRole("alert") once the page
      // adopts that.
      await expect(
        page.locator(".text-destructive, [role='alert']").first(),
      ).toBeVisible({ timeout: 5_000 });
      // No session cookie was minted on the failed attempt —
      // proves the backend rejected, not the UI silently
      // swallowed a 200.
      const cookies = await context.cookies();
      const sessionCookieNames = cookies
        .map((c) => c.name.toLowerCase())
        .filter((n) => n.includes("refresh") || n.includes("session"));
      expect(sessionCookieNames).toEqual([]);
    } finally {
      await context.close();
    }
  });

  // The client-side AuthGate redirects too, so the deep-link test above
  // passes even when the server-side proxy never runs. This one does not:
  // only src/proxy.ts refuses a foreign Origin, and Next has shipped a
  // standalone build that compiled the proxy without executing it.
  test("cross-origin POST to /api is refused by the proxy", async ({
    request,
    baseURL,
  }) => {
    const foreign = await request.post(UNSAFE_API_ROUTE, {
      headers: { Origin: FOREIGN_ORIGIN },
    });
    expect(foreign.status()).toBe(CSRF_STATUS);
    expect((await foreign.json()).message).toBe(CSRF_MESSAGE);

    // Same request from our own origin passes the gate: the refusal above
    // is about the Origin, not about the route.
    const own = await request.post(UNSAFE_API_ROUTE, {
      headers: { Origin: new URL(baseURL!).origin },
    });
    expect(own.status()).not.toBe(CSRF_STATUS);
  });
});
