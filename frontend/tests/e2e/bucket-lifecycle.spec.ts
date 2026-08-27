/**
 * Bucket lifecycle rules — the rule editor.
 *
 * SetLifecycleRules is OCC-guarded like the other bucket settings, and the
 * rules it writes delete data on a timer. A rule saved with the wrong match
 * expression or the wrong window is not a display bug; it is objects removed
 * that nobody asked to remove.
 *
 * The match field is CEL, validated server-side before the save — so the
 * editor has a failure mode the other settings pages do not: a syntactically
 * valid form carrying an expression the server will reject.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import {
  seedAdminTenantID,
  bucketLifecycle,
  bumpBucketVersion,
} from "./fixtures/seed";
import { gotoSettled } from "./fixtures/navigate";

function lifecycleURL(tenantId: string, backendId: string, bucketId: string) {
  return `/tenants/${tenantId}/buckets/${backendId}/${bucketId}/lifecycle`;
}

/**
 * Open the rule editor.
 *
 * The click is forced because the page re-renders as its data settles, so
 * Playwright's actionability wait never sees the button hold still — it is
 * visible and enabled throughout, just moving. Forcing skips the stability
 * wait, not the visibility one, and waiting for the dialog afterwards keeps
 * the failure honest if the click really did miss.
 */
async function openRuleEditor(page: import("@playwright/test").Page) {
  const addRule = page.getByRole("button", { name: /Add rule/ }).first();
  await expect(addRule).toBeVisible({ timeout: 20_000 });
  await addRule.click({ force: true });
  await expect(page.getByLabel("Rule ID")).toBeVisible({ timeout: 15_000 });
}

test.describe("Bucket lifecycle rules", () => {
  test("a bucket with no rules says so", async ({ page, makeBucket }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();

    await gotoSettled(
      page,
      lifecycleURL(tenantId, bucket.backendId, bucket.bucketId),
    );

    await expect(page.getByText(/No lifecycle rules/i)).toBeVisible({
      timeout: 15_000,
    });
  });

  test("creating a rule persists it and advances the version", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();
    const before = await bucketLifecycle(bucket.backendId, bucket.bucketId);
    expect(before.ruleCount).toBe(0);

    await gotoSettled(
      page,
      lifecycleURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    await openRuleEditor(page);

    await page.getByLabel("Rule ID").fill("e2e-expire-tmp");
    await page.getByLabel("Match (CEL)").fill("size_bytes > 1000");
    // Action is a pair of toggles with neither preselected, so a rule saved
    // without picking one has no action at all.
    await page.getByRole("button", { name: /^Expiration/ }).click();
    await page.getByRole("spinbutton", { name: "After" }).fill("30");
    await page.getByRole("button", { name: /^Create rule$/ }).click();

    await expect
      .poll(
        async () =>
          (await bucketLifecycle(bucket.backendId, bucket.bucketId)).ruleCount,
        { timeout: 15_000 },
      )
      .toBe(1);

    const after = await bucketLifecycle(bucket.backendId, bucket.bucketId);
    expect(after.resourceVersion).not.toBe(before.resourceVersion);
    await expect(page.getByText("e2e-expire-tmp")).toBeVisible();
  });

  test("an invalid CEL match is refused before it can delete anything", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();

    await gotoSettled(
      page,
      lifecycleURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    await openRuleEditor(page);

    await page.getByLabel("Rule ID").fill("e2e-bad-match");
    // A field the Object schema does not declare. Accepting this would store
    // a rule whose match never evaluates the way its author expected.
    await page.getByLabel("Match (CEL)").fill('not_a_field == "x"');
    await page.getByRole("button", { name: /^Expiration/ }).click();
    await page.getByRole("spinbutton", { name: "After" }).fill("30");

    const submit = page.getByRole("button", { name: /^Create rule$/ });
    await submit.click();

    // Either the editor blocks submission outright or the server refuses it —
    // both are acceptable, storing the rule is not.
    await expect
      .poll(
        async () =>
          (await bucketLifecycle(bucket.backendId, bucket.bucketId)).ruleCount,
        { timeout: 10_000 },
      )
      .toBe(0);
  });

  test("a bucket changed underneath the editor is refused", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();

    await gotoSettled(
      page,
      lifecycleURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    await openRuleEditor(page);
    await page.getByLabel("Rule ID").fill("e2e-stale-write");
    await page.getByLabel("Match (CEL)").fill("size_bytes > 1000");
    await page.getByRole("button", { name: /^Expiration/ }).click();
    await page.getByRole("spinbutton", { name: "After" }).fill("30");

    // The page read its version when it loaded; move it before saving.
    await bumpBucketVersion(bucket.backendId, bucket.bucketId);
    const afterConcurrent = await bucketLifecycle(
      bucket.backendId,
      bucket.bucketId,
    );

    await page.getByRole("button", { name: /^Create rule$/ }).click();

    // The toast says "Save failed" but auto-dismisses, so asserting on it
    // races the assertion. The refusal is observable in the data instead: no
    // rule stored, version untouched.
    await page.waitForTimeout(3_000);
    const now = await bucketLifecycle(bucket.backendId, bucket.bucketId);
    expect(now.ruleCount).toBe(0);
    expect(now.resourceVersion).toBe(afterConcurrent.resourceVersion);
  });

  test("the match placeholder is itself a valid expression", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();

    await gotoSettled(
      page,
      lifecycleURL(tenantId, bucket.backendId, bucket.bucketId),
    );
    await openRuleEditor(page);

    // A placeholder in a CEL field is a worked example — an operator copies
    // it. It carried `object.size_bytes > 1_000_000`, which is wrong twice:
    // the schema declares bare identifiers, and CEL has no digit separators.
    // Typing the suggestion verbatim got two compile errors.
    const match = page.getByLabel("Match (CEL)");
    const suggestion = await match.getAttribute("placeholder");
    expect(suggestion, "the field must suggest something").toBeTruthy();

    await page.getByLabel("Rule ID").fill("e2e-placeholder");
    await match.fill(suggestion as string);
    await page.getByRole("button", { name: /^Expiration/ }).click();
    await page.getByRole("spinbutton", { name: "After" }).fill("30");
    await page.getByRole("button", { name: /^Create rule$/ }).click();

    await expect
      .poll(
        async () =>
          (await bucketLifecycle(bucket.backendId, bucket.bucketId)).ruleCount,
        { timeout: 15_000 },
      )
      .toBe(1);
  });
});
