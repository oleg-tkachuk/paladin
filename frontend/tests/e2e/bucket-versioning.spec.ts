/**
 * Bucket versioning — a mutating settings page with an OCC guard.
 *
 * SetVersioning requires resource_version (min_len=1 in the schema), so every
 * save carries the version the page read. None of the bucket settings pages —
 * versioning, object-lock, lifecycle, replication — had any e2e, which is how
 * the quota form's "submit enabled before the data loaded" defect survived on
 * a sibling page.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import {
  seedAdminTenantID,
  seedBucket,
  bucketState,
  setBucketVersioning,
} from "./fixtures/seed";
import { gotoSettled } from "./fixtures/navigate";

function versioningURL(tenantId: string, backendId: string, bucketId: string) {
  return `/tenants/${tenantId}/buckets/${backendId}/${bucketId}/versioning`;
}

test.describe("Bucket versioning", () => {
  test("enabling versioning persists and advances the version", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedBucket();
    const before = await bucketState(bucket.backendId, bucket.bucketId);
    expect(before.versioningEnabled).toBe(false);

    await gotoSettled(
      page,
      versioningURL(tenantId, bucket.backendId, bucket.bucketId),
    );

    // Save starts disabled — nothing is dirty yet. That it becomes enabled
    // only after a change is itself the guard against submitting a form whose
    // resource_version has not loaded.
    const save = page.getByRole("button", { name: /^Save$/ });
    await expect(save).toBeDisabled({ timeout: 15_000 });

    await page.getByRole("switch", { name: /Enable versioning/i }).click();
    await expect(save).toBeEnabled();
    await save.click();

    await expect
      .poll(
        async () =>
          (await bucketState(bucket.backendId, bucket.bucketId))
            .versioningEnabled,
        { timeout: 15_000 },
      )
      .toBe(true);

    const after = await bucketState(bucket.backendId, bucket.bucketId);
    expect(after.resourceVersion).not.toBe(before.resourceVersion);
  });

  test("keep-deletes is gated on versioning being on", async ({ page }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedBucket();

    await gotoSettled(
      page,
      versioningURL(tenantId, bucket.backendId, bucket.bucketId),
    );

    // Retaining delete markers is meaningless without versioning, and the
    // server would store a combination it never honours. The UI refuses to
    // offer it rather than accepting and ignoring it.
    const keepDeletes = page.getByRole("switch", {
      name: /Keep deletes forever/i,
    });
    await expect(keepDeletes).toBeDisabled({ timeout: 15_000 });

    await page.getByRole("switch", { name: /Enable versioning/i }).click();
    await expect(keepDeletes).toBeEnabled();
  });

  test("a bucket changed underneath the form is refused", async ({ page }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedBucket();

    await gotoSettled(
      page,
      versioningURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    const save = page.getByRole("button", { name: /^Save$/ });
    await expect(save).toBeDisabled({ timeout: 15_000 });

    // Someone else flips versioning on. The page's held version is stale.
    await setBucketVersioning({
      backendId: bucket.backendId,
      bucketId: bucket.bucketId,
      enabled: true,
    });
    const afterConcurrent = await bucketState(
      bucket.backendId,
      bucket.bucketId,
    );

    await page.getByRole("switch", { name: /Enable versioning/i }).click();
    await save.click();

    await expect(page.getByText(/Save failed/i)).toBeVisible({
      timeout: 15_000,
    });

    // The refusal must be real: the concurrent writer's state stands.
    const now = await bucketState(bucket.backendId, bucket.bucketId);
    expect(now.resourceVersion).toBe(afterConcurrent.resourceVersion);
  });
});
