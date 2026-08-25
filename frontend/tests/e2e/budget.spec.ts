/**
 * Tenant budget — the aggregate spend cap.
 *
 * Unlike quotas, TenantBudgetService.Set carries NO resource_version: it is a
 * plain upsert, so two operators editing the same cap race with last-write-
 * wins and neither is told. That is recorded in BACKLOG rather than fixed
 * here; these tests cover what the page does today.
 *
 * The flag worth pinning is reset_spend. Set(false) changes the cap mid-window
 * and leaves the counter alone; Set(true) rolls the period and zeroes it.
 * Getting that backwards either loses a tenant's accrued spend or refuses
 * charges it should still accept.
 */
import { test, expect } from "./fixtures/tenants";
import { loginAsAdmin } from "./fixtures/auth";
import { tenantBudget } from "./fixtures/seed";
import { gotoSettled } from "./fixtures/navigate";

function budgetURL(tenantId: string): string {
  return `/tenants/${tenantId}/budget`;
}

/**
 * Submit the budget form.
 *
 * Forced for the same reason as the other console pages: the layout settles
 * after the spend summary loads, so the button is visible and enabled but not
 * still, and Playwright's actionability wait times out on a click that would
 * have landed. Waiting for the effect afterwards keeps the failure honest.
 */
async function submitBudget(
  page: import("@playwright/test").Page,
  name: RegExp,
) {
  const btn = page.getByRole("button", { name });
  await expect(btn).toBeVisible({ timeout: 20_000 });
  await expect(btn).toBeEnabled({ timeout: 20_000 });
  await btn.click({ force: true });
}

test.describe("Tenant budget", () => {
  test("a tenant with no budget offers to create one", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    expect(await tenantBudget(tenant.tenantId)).toBeNull();

    await gotoSettled(page, budgetURL(tenant.tenantId));

    // "Create budget" rather than "Apply changes" is the page saying it holds
    // no row — the same create/update distinction the quota form makes.
    await expect(
      page.getByRole("button", { name: /Create budget/ }),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("setting a cap persists it", async ({ page, makeTenant }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();

    await gotoSettled(page, budgetURL(tenant.tenantId));
    const submit = page.getByRole("button", { name: /Create budget/ });
    await expect(submit).toBeVisible({ timeout: 15_000 });

    await page.locator("#max-budget").fill("250");
    await submitBudget(page, /Create budget/);

    await expect
      .poll(
        async () => (await tenantBudget(tenant.tenantId))?.maxBudgetAmount,
        {
          timeout: 15_000,
        },
      )
      .toBe(250);
  });

  test("Save is held until a cap is entered", async ({ page, makeTenant }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();

    await gotoSettled(page, budgetURL(tenant.tenantId));
    const submit = page.getByRole("button", { name: /Create budget/ });
    await expect(submit).toBeVisible({ timeout: 15_000 });

    // An empty cap is not "unlimited" — it is nothing to send. The form holds
    // rather than posting a value it would have to invent.
    await page.locator("#max-budget").fill("");
    await expect(submit).toBeDisabled();
  });

  test("editing an existing budget keeps the spend counter", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();

    // First save creates the row.
    await gotoSettled(page, budgetURL(tenant.tenantId));
    await expect(
      page.getByRole("button", { name: /Create budget/ }),
    ).toBeVisible({ timeout: 15_000 });
    await page.locator("#max-budget").fill("100");
    await submitBudget(page, /Create budget/);

    await expect
      .poll(
        async () => (await tenantBudget(tenant.tenantId))?.maxBudgetAmount,
        {
          timeout: 15_000,
        },
      )
      .toBe(100);
    const afterCreate = await tenantBudget(tenant.tenantId);

    // Second save raises the cap without rolling the period. reset_spend
    // defaults off, so spent_amount must survive — a cap change mid-window is
    // not a new accounting period.
    await page.reload();
    const apply = page.getByRole("button", { name: /Apply changes/ });
    await expect(apply).toBeVisible({ timeout: 15_000 });
    await page.locator("#max-budget").fill("500");
    await submitBudget(page, /Apply changes/);

    await expect
      .poll(
        async () => (await tenantBudget(tenant.tenantId))?.maxBudgetAmount,
        {
          timeout: 15_000,
        },
      )
      .toBe(500);
    expect((await tenantBudget(tenant.tenantId))?.spentAmount).toBe(
      afterCreate?.spentAmount,
    );
  });
});
