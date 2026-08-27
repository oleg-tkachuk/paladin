/**
 * Per-collection Cedar policy editor — the page that actually writes an
 * authorization policy onto an entity.
 *
 * policies.spec.ts covers the platform-wide editor at /policies, and covers it
 * only as far as Validate: no test there presses Save, so until now nothing
 * exercised SetCollectionPolicy through a browser at all. That is the call
 * that changes who may touch a tenant's data, and it is OCC-guarded, so both
 * halves matter — that a save lands the text the operator typed, and that a
 * save which cannot land says so instead of appearing to succeed.
 *
 * Every assertion about persistence reads the value back through the admin
 * API rather than trusting the page: a toast is what the UI believes, not
 * what the server stored.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import { seedAdminTenantID, collectionPolicy } from "./fixtures/seed";

const EDITOR = /permit \( principal, action, resource \)/;
const VALID_POLICY = `permit(principal, action, resource);`;

function policyURL(tenantId: string, collection: string): string {
  return `/tenants/${tenantId}/collections/${encodeURIComponent(collection)}/policy`;
}

test.describe("Collection policy editor", () => {
  test("saving writes the policy the operator typed", async ({
    page,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();
    const collection = await makeCollection({ tenantId, bucket });

    const before = await collectionPolicy(tenantId, collection.collection);
    expect(before.cedarPolicy).toBe("");

    await gotoSettled(page, policyURL(tenantId, collection.collection));
    const editor = page.getByPlaceholder(EDITOR);
    await expect(editor).toBeVisible({ timeout: 15_000 });

    await editor.fill(VALID_POLICY);
    await page.getByRole("button", { name: /^Save$/ }).click();
    await expect(page.getByText(/Policy saved/i)).toBeVisible({
      timeout: 15_000,
    });

    // The assertion that matters: the server holds the new text, and the
    // write advanced the version — a save that returned the row unchanged
    // would satisfy the toast and nothing else.
    await expect
      .poll(
        async () =>
          (await collectionPolicy(tenantId, collection.collection)).cedarPolicy,
        { timeout: 15_000 },
      )
      .toBe(VALID_POLICY);
    const after = await collectionPolicy(tenantId, collection.collection);
    expect(after.resourceVersion).not.toBe(before.resourceVersion);
  });

  test("an unparseable policy is refused rather than stored", async ({
    page,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();
    const collection = await makeCollection({ tenantId, bucket });

    await gotoSettled(page, policyURL(tenantId, collection.collection));
    const editor = page.getByPlaceholder(EDITOR);
    await expect(editor).toBeVisible({ timeout: 15_000 });

    // Cedar needs all three clauses. Text the engine cannot compile must not
    // become the live policy — an entity whose policy fails to parse is an
    // entity whose authorization is undefined.
    await editor.fill("permit(principal);");
    await page.getByRole("button", { name: /^Save$/ }).click();

    await expect(page.getByText(/Save failed/i)).toBeVisible({
      timeout: 15_000,
    });
    // Refused, and refused all the way down: nothing was written.
    const stored = await collectionPolicy(tenantId, collection.collection);
    expect(stored.cedarPolicy).toBe("");
  });

  test("Validate reports a diagnostic without touching the stored policy", async ({
    page,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await makeBucket();
    const collection = await makeCollection({ tenantId, bucket });

    await gotoSettled(page, policyURL(tenantId, collection.collection));
    const editor = page.getByPlaceholder(EDITOR);
    await expect(editor).toBeVisible({ timeout: 15_000 });

    await editor.fill("permit(principal);");
    await page.getByRole("button", { name: /^Validate$/ }).click();

    await expect(
      page.getByText(/error|invalid|expected|unexpected/i).first(),
    ).toBeVisible({ timeout: 15_000 });
    // Validate is a dry run. The distinction is the reason the button exists.
    const stored = await collectionPolicy(tenantId, collection.collection);
    expect(stored.cedarPolicy).toBe("");
  });
});
