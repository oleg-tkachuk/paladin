/**
 * Tenant quotas — the OCC guard on SetQuota.
 *
 * SetQuota used to upsert without consulting quotas.resource_version, so two
 * operators editing the same tenant's limits raced: last write won, the loser
 * was told it succeeded, and the caps became whichever request landed second.
 * The guard now lives in the upsert's DO UPDATE clause and the field is
 * required by the schema.
 *
 * The console sends "0" when it holds no quota — that is the create case, and
 * it is a conflict if a row already exists. These tests drive the real form so
 * that contract is exercised end to end rather than at the repository seam.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { seedTenant, seedQuota, quotaVersion } from "./fixtures/seed";

function quotasURL(tenantId: string): string {
  return `/tenants/${tenantId}/quotas`;
}

test.describe("Tenant quotas — OCC", () => {
  test("a tenant with no quota offers Create, and creating one succeeds", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();

    await page.goto(quotasURL(tenant.tenantId));

    // "Create quota" rather than "Apply changes" is the console saying it
    // holds no row and will send resource_version "0".
    const submit = page.getByRole("button", { name: /Create quota/ });
    await expect(submit).toBeEnabled({ timeout: 15_000 });

    await page.getByLabel("Max total bytes").fill("2048");
    await page.getByLabel("Max object count").fill("10");
    await submit.click();

    // The row exists now, so the form must have switched out of create mode.
    await expect(
      page.getByRole("button", { name: /Apply changes/ }),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("editing an existing quota advances its resource version", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();
    const seeded = await seedQuota({ tenantId: tenant.tenantId });

    await page.goto(quotasURL(tenant.tenantId));
    // Enabled, not merely visible: the button stays disabled until the quota
    // has loaded, because the form's resource_version comes from it. Clicking
    // early would send "0" — "no row exists" — and be refused.
    const submit = page.getByRole("button", { name: /Apply changes/ });
    await expect(submit).toBeEnabled({ timeout: 15_000 });

    await page.getByLabel("Max object count").fill("4242");
    await submit.click();

    // Poll the version rather than the toast. A successful write must move
    // the version — if it did not, the guard could never reject anything and
    // the whole mechanism would be inert. The toast says the same thing but
    // auto-dismisses, so asserting on it races the assertion.
    await expect
      .poll(() => quotaVersion(tenant.tenantId), { timeout: 15_000 })
      .not.toBe(seeded.resourceVersion);
  });

  test("a quota changed underneath the form is refused, not overwritten", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();
    await seedQuota({ tenantId: tenant.tenantId, maxObjectCount: BigInt(100) });

    // Load the form — the console now holds the version it just read.
    await page.goto(quotasURL(tenant.tenantId));
    // Enabled, not merely visible: the button stays disabled until the quota
    // has loaded, because the form's resource_version comes from it. Clicking
    // early would send "0" — "no row exists" — and be refused.
    const submit = page.getByRole("button", { name: /Apply changes/ });
    await expect(submit).toBeEnabled({ timeout: 15_000 });

    // Someone else edits the same quota. The page's held version is stale
    // from here on; this is the race the guard exists for.
    await seedQuota({ tenantId: tenant.tenantId, maxObjectCount: BigInt(777) });
    const beforeSubmit = await quotaVersion(tenant.tenantId);

    await page.getByLabel("Max object count").fill("999");
    await submit.click();

    // The refusal has to be real: the concurrent writer's value stands and the
    // version has not moved. A UI that showed an error while the write landed
    // anyway would be worse than no guard at all.
    // Give the click time to reach the server and be refused before checking
    // that nothing moved; asserting immediately would pass even if the write
    // were still in flight.
    await page.waitForTimeout(2_000);
    expect(await quotaVersion(tenant.tenantId)).toBe(beforeSubmit);
  });
});
