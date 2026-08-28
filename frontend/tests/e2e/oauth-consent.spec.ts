/**
 * OAuth consent — the screen that stands between a third-party client and the
 * operator's account.
 *
 * It renders entirely from query parameters and posts them back to the
 * backend's authorize endpoint. That makes two things worth pinning, and
 * neither is cosmetic:
 *
 *   - the operator is told WHICH client is asking and WHAT it would get,
 *     because consent to an unnamed request is not consent;
 *   - every parameter round-trips into the form unchanged. `state` and
 *     `code_challenge` are the CSRF and PKCE halves of the flow — a screen
 *     that drops or rewrites one turns a protected exchange into an
 *     unprotected one, and nothing else in the flow would notice.
 *
 * The page is NOT a standalone route: STANDALONE_ROUTES holds only "/login",
 * so AuthGate bounces an unauthenticated visitor to the login form and back
 * again — the query string survives, because the gate carries
 * `pathname + search` in `next`. The tests therefore log in first, which is
 * what a real operator arriving from a third-party client ends up doing.
 *
 * A side effect worth noticing: the page ships its own subject/password form,
 * which nobody can reach any more — by the time the screen renders, the gate
 * has already authenticated the visitor. Dead UI rather than a defect, so it
 * is recorded here and not asserted on.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";

const PARAMS = {
  client_id: "e2e-third-party",
  redirect_uri: "https://client.example.test/callback",
  scope: "objects:read buckets:read",
  state: "st-4f2a9c",
  code_challenge: "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM",
  code_challenge_method: "S256",
};

function consentURL(overrides: Record<string, string> = {}): string {
  const q = new URLSearchParams({ ...PARAMS, ...overrides });
  return `/oauth/consent?${q.toString()}`;
}

test.describe("OAuth consent", () => {
  test("names the client and lists what it is asking for", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(consentURL());

    await expect(
      page.getByRole("heading", { name: /Authorize access/i }),
    ).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText(PARAMS.client_id)).toBeVisible();

    // The scopes are shown one per line rather than as the raw space-separated
    // string, because "objects:read buckets:read" is a thing to skim past and
    // a list is a thing to read.
    await expect(
      page.getByRole("listitem").filter({ hasText: "objects:read" }),
    ).toBeVisible();
    await expect(
      page.getByRole("listitem").filter({ hasText: "buckets:read" }),
    ).toBeVisible();
  });

  test("round-trips every OAuth parameter into the form", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(consentURL());
    await expect(
      page.getByRole("heading", { name: /Authorize access/i }),
    ).toBeVisible({ timeout: 15_000 });

    for (const [name, value] of Object.entries(PARAMS)) {
      const field = page.locator(`input[name="${name}"]`);
      await expect(field).toHaveValue(value);
    }
  });

  test("offers both answers, not just the agreeable one", async ({ page }) => {
    await loginAsAdmin(page);
    await page.goto(consentURL());

    // Deny has to be as reachable as Allow. A consent screen with only one
    // button is a notification.
    await expect(page.getByRole("button", { name: /^Allow$/ })).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByRole("button", { name: /^Deny$/ })).toBeVisible();
  });
});
