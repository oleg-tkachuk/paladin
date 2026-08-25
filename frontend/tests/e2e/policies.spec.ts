/**
 * Cedar policy editor — the page that decides who can do what.
 *
 * SetCollectionPolicy and SetBucketPolicy are OCC-guarded, and the text they
 * write is the authorization policy itself: a save that lands the wrong text,
 * or lands text the engine cannot compile, changes who is allowed to touch
 * data. The editor has a Validate button precisely because "it saved" is not
 * the same as "it means what you think".
 *
 * The tenant-level editor lives under /tenants/:id/policies; this covers the
 * platform-wide page at /policies, which can target any scope.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import { seedAdminTenantID, seedBucket, seedCollection } from "./fixtures/seed";

const VALID_POLICY = `permit(principal, action, resource);`;

/**
 * Select the first available target so the editor unlocks.
 *
 * Scope defaults to Tenant; the Target combobox is populated from it. Until
 * one is chosen the textarea stays disabled, which is the behaviour the first
 * test pins — every other test here has to get past it.
 */
async function pickFirstTarget(page: import("@playwright/test").Page) {
  const editor = page.getByLabel("Cedar policy");
  await expect(editor).toBeDisabled({ timeout: 15_000 });

  // Two comboboxes precede the template picker: scope, then target.
  const target = page.getByRole("combobox").nth(1);
  await target.click();
  await page.getByRole("option").first().click();

  await expect(editor).toBeEnabled({ timeout: 15_000 });
}

test.describe("Cedar policy editor", () => {
  test("the page loads with a scope and target picker", async ({ page }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/policies");

    await expect(page.getByText(/^Scope$/)).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText(/^Target$/)).toBeVisible();
    await expect(page.getByLabel("Cedar policy")).toBeVisible();
  });

  test("the editor is inert until a target is chosen", async ({ page }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/policies");

    // Stronger than a disabled Save: the textarea itself is locked, so a
    // policy cannot even be drafted with nowhere to put it. The placeholder
    // says why, rather than leaving the operator to guess at a dead field.
    const editor = page.getByLabel("Cedar policy");
    await expect(editor).toBeDisabled({ timeout: 15_000 });
    await expect(editor).toHaveAttribute("placeholder", /pick a target/i);
    await expect(page.getByRole("button", { name: /^Save$/ })).toBeDisabled();
  });

  test("Validate reports a broken policy without saving it", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedBucket();
    await seedCollection({ tenantId, bucket });

    await gotoSettled(page, "/policies");
    await pickFirstTarget(page);

    // Cedar, not CEL: `permit` needs its three clauses. The point of Validate
    // is to say so before the text becomes the live authorization policy.
    await page.getByLabel("Cedar policy").fill("permit(principal);");
    await page.getByRole("button", { name: /^Validate$/ }).click();

    // Some diagnostic has to surface. Which words the engine chooses are its
    // business; that the editor shows them rather than swallowing them is the
    // contract.
    await expect(
      page.getByText(/error|invalid|expected|unexpected/i).first(),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("Validate accepts a well-formed policy", async ({ page }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/policies");
    await pickFirstTarget(page);

    await page.getByLabel("Cedar policy").fill(VALID_POLICY);
    const validate = page.getByRole("button", { name: /^Validate$/ });
    await expect(validate).toBeEnabled();
    await validate.click();

    // A valid policy must not produce a diagnostic — a validator that flags
    // everything is as useless as one that flags nothing.
    await page.waitForTimeout(3_000);
    await expect(page.getByText(/unexpected|syntax error/i)).toHaveCount(0);
  });
});
