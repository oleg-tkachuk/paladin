/**
 * Event subscriptions — the fan-out configuration.
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

  test("creating an HTTP subscription persists it", async ({ page }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();
    expect(await subscriptionCount(tenant.tenantId)).toBe(0);

    await page.goto(subsURL(tenant.tenantId));
    const newSub = page
      .getByRole("button", { name: /New subscription/ })
      .first();
    await expect(newSub).toBeEnabled({ timeout: 15_000 });
    await newSub.click();
    await expect(page.locator("#sub-http-url")).toBeVisible({
      timeout: 15_000,
    });

    await page
      .locator("#sub-http-url")
      .fill("https://hooks.example.invalid/e2e");
    await page
      .getByRole("dialog")
      .getByRole("button", { name: /^Create$/ })
      .click();

    await expect
      .poll(() => subscriptionCount(tenant.tenantId), { timeout: 15_000 })
      .toBe(1);
  });

  test("an invalid CEL filter is refused", async ({ page }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();

    await page.goto(subsURL(tenant.tenantId));
    const newSub = page
      .getByRole("button", { name: /New subscription/ })
      .first();
    await expect(newSub).toBeEnabled({ timeout: 15_000 });
    await newSub.click();
    await expect(page.locator("#sub-http-url")).toBeVisible({
      timeout: 15_000,
    });

    await page
      .locator("#sub-http-url")
      .fill("https://hooks.example.invalid/e2e");
    // A field the EventEnvelope schema does not declare. Storing this would
    // produce a subscription whose filter never matches what its author meant.
    await page.locator("#sub-filter").fill('not_a_field == "x"');

    // The dialog validates the expression as it is typed and disables Create
    // outright — stronger than letting the server refuse it, because the
    // operator finds out before the round trip.
    //
    // The check is a round trip to CELService, so the refusal arrives
    // asynchronously and its latency tracks how busy the plane is. Inside the
    // full suite — twenty minutes of continuous traffic — 15s was not always
    // enough, and this test failed only there, never on its own. Poll with
    // headroom rather than asserting on one moment.
    const create = page
      .getByRole("dialog")
      .getByRole("button", { name: /^Create$/ });
    await expect
      .poll(() => create.isDisabled(), { timeout: 30_000 })
      .toBe(true);

    // And the refusal has to be real, not just a greyed-out button.
    expect(await subscriptionCount(tenant.tenantId)).toBe(0);
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

  test("the filter hint's examples are valid expressions", async ({ page }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();

    await page.goto(subsURL(tenant.tenantId));
    const newSub = page
      .getByRole("button", { name: /New subscription/ })
      .first();
    await expect(newSub).toBeEnabled({ timeout: 15_000 });
    await newSub.click();
    await expect(page.locator("#sub-http-url")).toBeVisible({
      timeout: 15_000,
    });

    // The hint is a worked example, so it has to work. It suggested
    // `event.kind == '...'` while the EventEnvelope schema declares bare
    // identifiers, so anyone copying it got "undeclared reference to 'event'"
    // — the same defect the lifecycle editor's placeholder had.
    const hint = await page
      .getByText(/Empty = all events\. Examples:/)
      .first()
      .textContent();
    expect(hint, "the filter field must suggest something").toBeTruthy();

    const first = /Examples:\s*([^,]+),/.exec(hint ?? "")?.[1]?.trim();
    expect(first, `could not read an example out of: ${hint}`).toBeTruthy();

    await page
      .locator("#sub-http-url")
      .fill("https://hooks.example.invalid/e2e");
    await page.locator("#sub-filter").fill(first as string);
    await page
      .getByRole("dialog")
      .getByRole("button", { name: /^Create$/ })
      .click();

    await expect
      .poll(() => subscriptionCount(tenant.tenantId), { timeout: 30_000 })
      .toBe(1);
  });
});
