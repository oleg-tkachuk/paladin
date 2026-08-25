/**
 * Bucket object lock (ADR-0013) — default retention for new objects.
 *
 * SetObjectLock is OCC-guarded like the other bucket settings. What makes it
 * worth its own file is the COMPLIANCE mode: retention under compliance cannot
 * be shortened or lifted by anyone, so a form that writes the wrong mode is
 * not a cosmetic bug — it is data nobody can delete until the window expires.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import {
  seedAdminTenantID,
  seedBucket,
  bucketLockState,
  bumpBucketVersion,
  setBucketVersioning,
} from "./fixtures/seed";
import { gotoSettled } from "./fixtures/navigate";

/** A bucket with versioning already on. Object lock refuses to engage without
 *  it (ADR-0013) — retention has nothing to pin to when a write replaces the
 *  object in place rather than adding a version. */
async function seedVersionedBucket() {
  const bucket = await seedBucket();
  await setBucketVersioning({
    backendId: bucket.backendId,
    bucketId: bucket.bucketId,
    enabled: true,
  });
  return bucket;
}

function lockURL(tenantId: string, backendId: string, bucketId: string) {
  return `/tenants/${tenantId}/buckets/${backendId}/${bucketId}/object-lock`;
}

test.describe("Bucket object lock", () => {
  test("enabling writes the mode and retention window", async ({ page }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedVersionedBucket();
    const before = await bucketLockState(bucket.backendId, bucket.bucketId);
    expect(before.enabled).toBe(false);

    await gotoSettled(
      page,
      lockURL(tenantId, bucket.backendId, bucket.bucketId),
    );

    const save = page.getByRole("button", { name: /^Save$/ });
    await expect(save).toBeDisabled({ timeout: 15_000 });

    await page.getByRole("switch", { name: /Enable object lock/i }).click();
    await page.getByLabel("Default retention (days)").fill("7");
    await expect(save).toBeEnabled();
    await save.click();

    await expect
      .poll(
        async () =>
          (await bucketLockState(bucket.backendId, bucket.bucketId)).enabled,
        { timeout: 15_000 },
      )
      .toBe(true);

    const after = await bucketLockState(bucket.backendId, bucket.bucketId);
    // 7 days as seconds — the form speaks days, the API speaks a Duration, and
    // a factor-of-anything slip here silently changes how long data is locked.
    expect(after.defaultRetentionSeconds).toBe(7 * 24 * 60 * 60);
    expect(after.resourceVersion).not.toBe(before.resourceVersion);
  });

  test("mode and retention are inert until lock is enabled", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedBucket();

    await gotoSettled(
      page,
      lockURL(tenantId, bucket.backendId, bucket.bucketId),
    );

    // Neither control means anything with the lock off, and offering them
    // would let an operator believe they had configured something.
    await expect(page.getByLabel("Default retention (days)")).toBeDisabled({
      timeout: 15_000,
    });
    await expect(
      page.getByRole("combobox", { name: /Default mode/i }),
    ).toBeDisabled();

    await page.getByRole("switch", { name: /Enable object lock/i }).click();
    await expect(page.getByLabel("Default retention (days)")).toBeEnabled();
  });

  test("a bucket changed underneath the form is refused", async ({ page }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedVersionedBucket();

    await gotoSettled(
      page,
      lockURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    const save = page.getByRole("button", { name: /^Save$/ });
    await expect(save).toBeDisabled({ timeout: 15_000 });

    await bumpBucketVersion(bucket.backendId, bucket.bucketId);
    const afterConcurrent = await bucketLockState(
      bucket.backendId,
      bucket.bucketId,
    );

    await page.getByRole("switch", { name: /Enable object lock/i }).click();
    await save.click();

    await expect(page.getByText(/Save failed/i)).toBeVisible({
      timeout: 15_000,
    });

    // Refused means refused: object lock must not have been switched on by a
    // write the server rejected.
    const now = await bucketLockState(bucket.backendId, bucket.bucketId);
    expect(now.enabled).toBe(false);
    expect(now.resourceVersion).toBe(afterConcurrent.resourceVersion);
  });

  test("object lock refuses to engage without versioning", async ({ page }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedBucket(); // versioning deliberately off

    await gotoSettled(
      page,
      lockURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    await page.getByRole("switch", { name: /Enable object lock/i }).click();
    await page.getByRole("button", { name: /^Save$/ }).click();

    // Retention has nothing to pin to when a write replaces the object in
    // place, so the server refuses rather than storing a lock it cannot
    // honour. The UI has to surface that instead of appearing to succeed.
    await expect(page.getByText(/Save failed/i)).toBeVisible({
      timeout: 15_000,
    });
    const after = await bucketLockState(bucket.backendId, bucket.bucketId);
    expect(after.enabled).toBe(false);
  });
});
