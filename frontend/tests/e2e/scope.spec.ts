/**
 * US2 — Backend + bucket scope switching.
 *
 * Spec: specs/001-frontend-playwright-e2e/spec.md §"User Story 2".
 *
 * Re-scoped during /speckit-implement from the original "tenant
 * switching" wording because the current UI's `<ScopePicker>`
 * holds tenant as a JWT-derived read-only field — cross-tenant
 * switching needs a backend SwitchTenant RPC that doesn't yet
 * exist. The cross-tenant work is tracked in
 * BACKLOG.md §"Cross-tenant scope switcher".
 *
 * Three scenarios exercise the scope axes the UI ACTUALLY
 * exposes today:
 *   1. picker opens; sections render with current state
 *   2. selecting a bucket flips the topbar breadcrumb label
 *   3. reload preserves the localStorage'd scope
 *
 * Per-test fixture: one seeded bucket under the default
 * `primary` backend. The picker needs at least one selectable
 * row for the swap test to be meaningful.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { seedBucket } from "./fixtures/seed";

// The ScopePicker is a two-level Radix popover (ScopePicker.tsx): the main
// trigger ("Scope picker — …") opens a popover holding two ScopeRow buttons —
// "Switch backend — …" and "Switch bucket — …". Each ScopeRow opens its OWN
// nested cmdk popover (CommandGroup heading "Backends"/"Buckets") with the
// selectable options. So bucket options aren't reachable from the main popover
// directly — you open the bucket sub-row first.
async function selectBucket(
  page: import("@playwright/test").Page,
  bucketName: string,
) {
  await page.getByRole("button", { name: /^Scope picker —/ }).click();
  await page.getByRole("button", { name: /^Switch bucket/ }).click();
  // Filter the cmdk list down to the seeded bucket — the list accumulates
  // buckets across runs, so searching keeps the option reliably rendered.
  await page.getByPlaceholder("Search buckets…").fill(bucketName);
  await page
    .getByRole("option", { name: new RegExp(bucketName) })
    .first()
    .click();
}

test.describe("US2 — Backend + bucket scope switching", () => {
  test("picker opens with backend + bucket switchers", async ({ page }) => {
    // Seed BEFORE login so the bucket exists when the picker's first
    // ListBuckets fetch runs (seeding after login races that fetch).
    await seedBucket();
    await loginAsAdmin(page);

    // The picker trigger has an aria-label starting with
    // "Scope picker —" (ScopePicker.tsx:130). Matching on the
    // prefix keeps the test resilient to the dynamic suffix.
    const trigger = page.getByRole("button", {
      name: /^Scope picker —/,
    });
    await expect(trigger).toBeVisible();
    await trigger.click();

    // The opened popover holds the two scope-axis ScopeRows. Their
    // aria-labels are "Switch backend — …" / "Switch bucket — …"
    // (ScopePicker.tsx:527); the "Backends"/"Buckets" group headings
    // live inside each row's nested popover, not here.
    await expect(
      page.getByRole("button", { name: /^Switch backend/ }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: /^Switch bucket/ }),
    ).toBeVisible();
  });

  test("selecting a bucket updates the topbar breadcrumb", async ({ page }) => {
    const bucket = await seedBucket();
    await loginAsAdmin(page);

    await selectBucket(page, bucket.bucketName);

    // Selecting closes the nested row popover; the main trigger's
    // aria-label now embeds the chosen bucket (ScopePicker.tsx:130 —
    // "… bucket <bucketName>, tenant <tenantLabel>").
    await expect(
      page.getByRole("button", { name: /^Scope picker —/ }),
    ).toHaveAttribute("aria-label", new RegExp(`bucket ${bucket.bucketName}`));
  });

  test("reload preserves the selected scope", async ({ page }) => {
    const bucket = await seedBucket();
    await loginAsAdmin(page);

    await selectBucket(page, bucket.bucketName);

    // Pre-reload sanity: scope is set.
    await expect(
      page.getByRole("button", { name: /^Scope picker —/ }),
    ).toHaveAttribute("aria-label", new RegExp(`bucket ${bucket.bucketName}`));

    // The localStorage write happens synchronously in
    // setBucket / setScope; reload must round-trip it.
    await page.reload();

    // Same locator after reload — re-resolves the topbar
    // post-hydration. If safeRead() in ScopeContext skipped
    // the localStorage key, the aria-label would fall back
    // to "bucket any" and the assertion fails.
    await expect(
      page.getByRole("button", { name: /^Scope picker —/ }),
    ).toHaveAttribute("aria-label", new RegExp(`bucket ${bucket.bucketName}`));
  });
});
