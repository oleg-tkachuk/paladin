/**
 * Per-bucket Cedar policy editor.
 *
 * The same write shape as the collection editor — read the entity, edit the
 * text, save under the version that was read — against a different RPC
 * (SetBucketPolicy) and a different entity. It is covered separately rather
 * than assumed: the guard that refuses uncompilable Cedar was added to every
 * policy-accepting shim at once precisely because a guard on one door is not a
 * guard, and only a test per door can say the doors agree.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import { seedAdminTenantID, bucketPolicy } from "./fixtures/seed";

const EDITOR = /permit \( principal, action, resource \)/;
const VALID_POLICY = `permit(principal, action, resource);`;

function policyURL(
  tenantId: string,
  backendId: string,
  bucketId: string,
): string {
  return `/tenants/${tenantId}/buckets/${backendId}/${bucketId}/policy`;
}

test.describe("Bucket policy editor", () => {
  test("saving writes the policy the operator typed", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();

    const before = await bucketPolicy(bucket.backendId, bucket.bucketId);
    expect(before.cedarPolicy).toBe("");

    await gotoSettled(
      page,
      policyURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    const editor = page.getByPlaceholder(EDITOR);
    await expect(editor).toBeVisible({ timeout: 15_000 });

    await editor.fill(VALID_POLICY);
    await page.getByRole("button", { name: /^Save$/ }).click();
    await expect(page.getByText(/Policy saved/i)).toBeVisible({
      timeout: 15_000,
    });

    await expect
      .poll(
        async () =>
          (await bucketPolicy(bucket.backendId, bucket.bucketId)).cedarPolicy,
        { timeout: 15_000 },
      )
      .toBe(VALID_POLICY);
    const after = await bucketPolicy(bucket.backendId, bucket.bucketId);
    expect(after.resourceVersion).not.toBe(before.resourceVersion);
  });

  test("an unparseable policy is refused rather than stored", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();

    await gotoSettled(
      page,
      policyURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    const editor = page.getByPlaceholder(EDITOR);
    await expect(editor).toBeVisible({ timeout: 15_000 });

    // A bucket whose policy does not compile is a bucket whose authorization
    // is undefined — and, before the write-side guard existed, one that could
    // no longer be read or deleted either.
    await editor.fill("permit(principal);");
    await page.getByRole("button", { name: /^Save$/ }).click();

    await expect(page.getByText(/Save failed/i)).toBeVisible({
      timeout: 15_000,
    });
    const stored = await bucketPolicy(bucket.backendId, bucket.bucketId);
    expect(stored.cedarPolicy).toBe("");
  });

  test("Validate reports a diagnostic without touching the stored policy", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();

    await gotoSettled(
      page,
      policyURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    const editor = page.getByPlaceholder(EDITOR);
    await expect(editor).toBeVisible({ timeout: 15_000 });

    await editor.fill("permit(principal);");
    await page.getByRole("button", { name: /^Validate$/ }).click();

    await expect(
      page.getByText(/error|invalid|expected|unexpected/i).first(),
    ).toBeVisible({ timeout: 15_000 });
    const stored = await bucketPolicy(bucket.backendId, bucket.bucketId);
    expect(stored.cedarPolicy).toBe("");
  });
});
