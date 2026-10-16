/**
 * Sharing a bucket (ADR-0032): the bucket page names its owner and lists the
 * tenants the owner granted it, and grants and revokes them.
 *
 * Sharing used to be the absence of an owner, open to every tenant; now it is
 * a decision someone made on this page. What it owes the operator is that a
 * grant reaches the server, that a revoke asks first, and that a revoke the
 * server refuses — the tenant still has collections there — says why rather
 * than leaving the grant looking revoked.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import { bucketGrantees, type SeededBucket } from "./fixtures/seed";

/** How long the page has to show what the server answered. */
const SETTLE_TIMEOUT_MS = 15_000;

function bucketPageURL(b: SeededBucket): string {
  return `/storage-backends/${encodeURIComponent(b.backendId)}/buckets/${encodeURIComponent(b.bucketId)}`;
}

/** How the page names a tenant: its display name, then its slug. */
function tenantLabel(t: { slug: string; displayName: string }): string {
  return `${t.displayName} (${t.slug})`;
}

test.describe("Bucket grants", () => {
  test("granting a tenant reaches the server and lists it", async ({
    page,
    makeTenant,
    makeBucket,
    trackGrant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant({ slugPrefix: "grantee" });
    // Owned by the platform tenant, as an operator's pool is.
    const bucket = await makeBucket();

    await gotoSettled(page, bucketPageURL(bucket));
    await expect(
      page.getByText(/only the owner binds collections here/),
    ).toBeVisible({ timeout: SETTLE_TIMEOUT_MS });

    await page.getByRole("combobox", { name: "Tenant to grant" }).click();
    await page.getByRole("option", { name: tenantLabel(tenant) }).click();
    // Registered before the click, so a grant that lands is revoked even if
    // an assertion below fails.
    trackGrant({ bucket, tenantId: tenant.tenantId });
    await page.getByRole("button", { name: "Grant", exact: true }).click();

    await expect(page.getByRole("link", { name: tenant.slug })).toBeVisible({
      timeout: SETTLE_TIMEOUT_MS,
    });
    await expect
      .poll(() => bucketGrantees(bucket), { timeout: SETTLE_TIMEOUT_MS })
      .toContain(tenant.tenantId);
  });

  test("revoking a grant asks first, then removes it", async ({
    page,
    makeTenant,
    makeBucket,
    makeGrant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant({ slugPrefix: "grantee" });
    const bucket = await makeBucket();
    await makeGrant(bucket, tenant.tenantId);

    await gotoSettled(page, bucketPageURL(bucket));
    await page
      .getByRole("button", { name: `Revoke ${tenantLabel(tenant)}` })
      .click({ timeout: SETTLE_TIMEOUT_MS });
    const dialog = page.getByRole("alertdialog");
    await expect(dialog.getByRole("note")).toContainText(
      /still has collections/,
    );
    await dialog.getByRole("button", { name: "Revoke grant" }).click();

    await expect
      .poll(() => bucketGrantees(bucket), { timeout: SETTLE_TIMEOUT_MS })
      .not.toContain(tenant.tenantId);
  });

  test("a revoke refused for the tenant's collections says why", async ({
    page,
    makeTenant,
    makeBucket,
    makeGrant,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant({ slugPrefix: "grantee" });
    const bucket = await makeBucket();
    await makeGrant(bucket, tenant.tenantId);
    // Bound through the grant; teardown removes it before the grant.
    await makeCollection({ tenantId: tenant.tenantId, bucket });

    await gotoSettled(page, bucketPageURL(bucket));
    await page
      .getByRole("button", { name: `Revoke ${tenantLabel(tenant)}` })
      .click({ timeout: SETTLE_TIMEOUT_MS });
    await page
      .getByRole("alertdialog")
      .getByRole("button", { name: "Revoke grant" })
      .click();

    await expect(
      page
        .getByRole("alert")
        .filter({ hasText: /still has collections in this bucket/ }),
    ).toBeVisible({ timeout: SETTLE_TIMEOUT_MS });
    expect(await bucketGrantees(bucket)).toContain(tenant.tenantId);
  });
});
