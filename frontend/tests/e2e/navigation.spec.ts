/**
 * Where a page is (ADR-0033): one URL per bucket, a badge naming the space a
 * page is in, and the sidebar lighting the section that owns the page.
 *
 * Each is decided by a pure function the unit tests pin; this checks they
 * reach the screen together, on the routes that used to disagree.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";

const bucketURL = (backendId: string, bucketId: string) =>
  `/storage-backends/${backendId}/buckets/${bucketId}`;

test.describe("Console navigation", () => {
  test("a bucket's old page under its owner lands on its one page", async ({
    page,
    makeTenant,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    const bucket = await makeBucket({ ownerTenantId: tenant.tenantId });

    await gotoSettled(
      page,
      `/tenants/${tenant.slug}/buckets/${bucket.backendId}/${bucket.bucketId}/lifecycle`,
    );

    await expect(page).toHaveURL(
      new RegExp(`${bucketURL(bucket.backendId, bucket.bucketId)}/lifecycle$`),
    );
    await expect(
      page.getByRole("tab", { name: "Lifecycle", selected: true }),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("a bucket is a platform page, lit as Buckets", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const bucket = await makeBucket();

    await gotoSettled(page, bucketURL(bucket.backendId, bucket.bucketId));

    await expect(page.locator('[data-scope="platform"]')).toBeVisible({
      timeout: 15_000,
    });
    // The sidebar's lit row; the trail's own current crumb is not a link.
    await expect(
      page.locator('a[aria-current="page"]', { hasText: /^Buckets$/ }),
    ).toBeVisible();
  });

  test("a tenant's workspace names the tenant", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();

    await gotoSettled(page, `/tenants/${tenant.slug}/collections`);

    const badge = page.locator('[data-scope="tenant"]');
    await expect(badge).toBeVisible({ timeout: 15_000 });
    await expect(badge).toContainText(tenant.displayName || tenant.slug);
  });
});
