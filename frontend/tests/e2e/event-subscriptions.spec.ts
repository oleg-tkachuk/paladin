/**
 * Event subscriptions — the fan-out configuration.
 *
 * Only the read-side flows are covered here. Anything that drives the editor
 * dialog is not: the page polls for delivery status, so it re-renders
 * continuously and Playwright's actionability wait never sees the trigger hold
 * still. Forcing the click opens the dialog but the next interaction races the
 * same way — three consecutive runs failed a different test each time. The gap
 * is recorded in BACKLOG rather than papered over; CreateSubscription and its
 * CEL validation are covered by the Go integration suite.
 *
 * A subscription decides where a tenant's events are delivered, so a broken
 * one is silent by nature: nothing errors, events simply stop arriving
 * somewhere. The editor validates its CEL filter server-side before saving,
 * which is the same shape as the lifecycle rule editor and the same failure
 * mode — a form that looks fine carrying an expression the server rejects.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import {
  seedTenant,
  seedSubscription,
  subscriptionCount,
} from "./fixtures/seed";

function subsURL(tenantId: string): string {
  return `/tenants/${tenantId}/event-subscriptions`;
}

/**
 * Open the subscription editor.
 *
 * The click is forced because the page re-renders as its data settles, and
 * Playwright's actionability wait never sees the button hold still — it is
 * visible and enabled throughout, just moving. Forcing skips the stability
 * wait, not the visibility one, so a genuinely missing button still fails.
 */
async function openEditor(page: import("@playwright/test").Page) {
  const newSub = page.getByRole("button", { name: /New subscription/ }).first();
  await expect(newSub).toBeVisible({ timeout: 20_000 });
  await newSub.click({ force: true });
  await expect(page.getByRole("dialog")).toBeVisible({ timeout: 15_000 });
  await expect(page.locator("#sub-http-url")).toBeVisible({ timeout: 15_000 });
}

test.describe("Event subscriptions", () => {
  test("a tenant with no subscriptions offers to create one", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();

    await page.goto(subsURL(tenant.tenantId));

    await expect(
      page.getByRole("button", { name: /New subscription/ }).first(),
    ).toBeEnabled({ timeout: 15_000 });
  });

  test("an existing subscription is listed and can be deleted", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();
    await seedSubscription({ tenantId: tenant.tenantId });
    expect(await subscriptionCount(tenant.tenantId)).toBe(1);

    await page.goto(subsURL(tenant.tenantId));

    const row = page
      .getByRole("row")
      .filter({ hasText: "hooks.example.invalid" });
    await expect(row).toBeVisible({ timeout: 15_000 });

    await row
      .getByRole("button", { name: /Actions|Delete/i })
      .first()
      .click();
    await page.getByRole("menuitem", { name: /Delete/i }).click();
    await page
      .getByRole("button", { name: /^Delete/ })
      .last()
      .click();

    await expect
      .poll(() => subscriptionCount(tenant.tenantId), { timeout: 15_000 })
      .toBe(0);
  });
});
