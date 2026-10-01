/**
 * Tenant default route — the page that decides where a bare-name creation
 * lands.
 *
 * A Collection created without naming a bucket is routed by this binding, and
 * a tenant with none rejects that creation outright. So the two states this
 * page moves between are not cosmetic: they are the difference between "new
 * data goes here" and "new data is refused". Nothing had ever opened it.
 *
 * Every assertion reads the binding back through the admin API. The card's own
 * text is what the console believes; the binding is what the server will act
 * on, and only the second one routes an object.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import {
  tenantDefaultBinding,
  clearTenantDefaultBinding,
} from "./fixtures/seed";

function bindingURL(tenantId: string): string {
  return `/tenants/${tenantId}/default-binding`;
}

test.describe("Tenant default route", () => {
  test("a tenant with no binding says so, and says what it costs", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    expect(await tenantDefaultBinding(tenant.tenantId)).toBe("");

    await gotoSettled(page, bindingURL(tenant.tenantId));

    // Not merely "none": the page states the consequence, because an operator
    // who does not know that bare-name creation is rejected will read an empty
    // field as "nothing to do here".
    await expect(
      page.getByText(/bare-name creation will be rejected/i),
    ).toBeVisible({ timeout: 15_000 });
    // Nothing to clear when nothing is bound.
    await expect(page.getByRole("button", { name: /^Clear$/ })).toHaveCount(0);
  });

  test("setting a route persists the bucket it names", async ({
    page,
    makeTenant,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    const bucket = await makeBucket({ ownerTenantId: tenant.tenantId });

    await gotoSettled(page, bindingURL(tenant.tenantId));

    const picker = page.getByLabel("Default bucket");
    await expect(picker).toBeVisible({ timeout: 15_000 });
    // The Set button stays inert until a bucket is chosen — the same guard the
    // quota form once lacked, and for the same reason: a submit that fires
    // before the data loaded writes whatever the empty form held.
    await expect(page.getByRole("button", { name: /^Set$/ })).toBeDisabled();

    await picker.selectOption({
      label: `${bucket.backendId} / ${bucket.bucketId}`,
    });
    const setButton = page.getByRole("button", { name: /^Set$/ });
    await expect(setButton).toBeEnabled();
    await setButton.click();

    await expect(page.getByText(/Default binding set/i)).toBeVisible({
      timeout: 15_000,
    });
    // The assertion that matters: the server routes to this bucket now.
    await expect
      .poll(async () => tenantDefaultBinding(tenant.tenantId), {
        timeout: 15_000,
      })
      .toBe(`storageBackends/${bucket.backendId}/buckets/${bucket.bucketId}`);

    // The binding holds a foreign key to the bucket, so leaving it set would
    // block the bucket's teardown and leak it. Clearing here is housekeeping,
    // not part of the assertion — the persistence check above has already run.
    await clearTenantDefaultBinding(tenant.tenantId);
  });

  test("clearing a route removes it rather than hiding it", async ({
    page,
    makeTenant,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    const bucket = await makeBucket({ ownerTenantId: tenant.tenantId });

    await gotoSettled(page, bindingURL(tenant.tenantId));
    const picker = page.getByLabel("Default bucket");
    await expect(picker).toBeVisible({ timeout: 15_000 });
    await picker.selectOption({
      label: `${bucket.backendId} / ${bucket.bucketId}`,
    });
    await page.getByRole("button", { name: /^Set$/ }).click();
    await expect(page.getByText(/Default binding set/i)).toBeVisible({
      timeout: 15_000,
    });

    // Clear only exists once something is bound — its presence is itself the
    // page's statement about the current state.
    const clear = page.getByRole("button", { name: /^Clear$/ });
    await expect(clear).toBeVisible({ timeout: 15_000 });
    await clear.click();
    // Every bare-name create in the tenant fails once the route is gone, so
    // Clear asks first.
    await page
      .getByRole("alertdialog")
      .getByRole("button", { name: "Clear route" })
      .click();

    await expect(page.getByText(/Default binding cleared/i)).toBeVisible({
      timeout: 15_000,
    });
    await expect
      .poll(async () => tenantDefaultBinding(tenant.tenantId), {
        timeout: 15_000,
      })
      .toBe("");
  });
});
