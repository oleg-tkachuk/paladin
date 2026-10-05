/**
 * A tenant's Policies tab shows the Cedar policy the authorizer evaluates for
 * it: the platform's built-in layer, then the tenant's own. It was a stub
 * pointing at /policies.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";

test("a tenant's Policies tab shows its effective policy", async ({
  page,
  makeTenant,
}) => {
  await loginAsAdmin(page);
  // A new tenant gets a default policy, so its layer has text to show.
  const tenant = await makeTenant();
  await gotoSettled(page, `/tenants/${tenant.tenantId}/policies`);

  await expect(
    page.getByRole("heading", { name: "Effective Cedar policy" }),
  ).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText("built-in", { exact: true })).toBeVisible({
    timeout: 15_000,
  });
  await expect(
    page.getByText(`tenants/${tenant.tenantId}`, { exact: true }),
  ).toBeVisible();
});

// Picking a collection shows the layers a request on it also meets — its
// bucket's and its own — even when, as for a fresh collection, they are
// empty. They used to be dropped, so the page did not change.
test("picking a collection adds its bucket's and its own layers", async ({
  page,
  makeTenant,
  makeBucket,
  makeCollection,
}) => {
  await loginAsAdmin(page);
  const tenant = await makeTenant();
  const bucket = await makeBucket();
  const collection = await makeCollection({
    tenantId: tenant.tenantId,
    bucket,
  });
  await gotoSettled(page, `/tenants/${tenant.tenantId}/policies`);

  await page.getByRole("combobox", { name: "Scope" }).click();
  await page
    .getByRole("option", { name: `Collection ${collection.collection}` })
    .click();
  await expect(
    page.getByText(
      `tenants/${tenant.tenantId}/collections/${collection.collection}`,
      { exact: true },
    ),
  ).toBeVisible({ timeout: 15_000 });
  await expect(
    page.getByText(collection.bucket, { exact: true }),
  ).toBeVisible();
});
