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

test.describe("US2 — Backend + bucket scope switching", () => {
  test("picker opens with Backends + Buckets sections", async ({ page }) => {
    await loginAsAdmin(page);
    await seedBucket(); // ensure ≥1 bucket is selectable

    // The picker trigger has an aria-label starting with
    // "Scope picker —" (ScopePicker.tsx:130). Matching on the
    // prefix keeps the test resilient to the dynamic suffix.
    const trigger = page.getByRole("button", {
      name: /^Scope picker —/,
    });
    await expect(trigger).toBeVisible();
    await trigger.click();

    // Popover sections are headed "Backends" and "Buckets"
    // (or "Buckets in <backend>" once a backend is selected).
    await expect(page.getByText(/^Backends$/)).toBeVisible();
    await expect(page.getByText(/^Buckets( in .+)?$/)).toBeVisible();
  });

  test("selecting a bucket updates the topbar breadcrumb", async ({ page }) => {
    await loginAsAdmin(page);
    const bucket = await seedBucket();

    const trigger = page.getByRole("button", { name: /^Scope picker —/ });
    await trigger.click();

    // Search the bucket row by its unique name (UUID-
    // suffixed → no collision with neighbouring tests).
    // The cmdk list items in ScopePicker render the
    // bucketName + displayName + backendId, all searchable.
    const bucketRow = page
      .getByRole("option", { name: new RegExp(bucket.bucketName) })
      .first();
    await bucketRow.click();

    // Popover closes after selection (setOpen(false) in cmdk
    // onSelect). The trigger's aria-label now embeds the
    // newly selected bucket name (ScopePicker.tsx:130 — the
    // pattern is "bucket <bucketName>, tenant <tenantLabel>").
    await expect(trigger).toHaveAttribute(
      "aria-label",
      new RegExp(`bucket ${bucket.bucketName}`),
    );
  });

  test("reload preserves the selected scope", async ({ page }) => {
    await loginAsAdmin(page);
    const bucket = await seedBucket();

    // Select the bucket through the picker first.
    const trigger = page.getByRole("button", { name: /^Scope picker —/ });
    await trigger.click();
    await page
      .getByRole("option", { name: new RegExp(bucket.bucketName) })
      .first()
      .click();

    // Pre-reload sanity: scope is set.
    await expect(trigger).toHaveAttribute(
      "aria-label",
      new RegExp(`bucket ${bucket.bucketName}`),
    );

    // The localStorage write happens synchronously in
    // setBucket / setScope; reload must round-trip it.
    await page.reload();

    // Same locator after reload — re-resolves the topbar
    // post-hydration. If safeRead() in ScopeContext skipped
    // the localStorage key, the aria-label would fall back
    // to "bucket any" and the assertion fails.
    const triggerAfterReload = page.getByRole("button", {
      name: /^Scope picker —/,
    });
    await expect(triggerAfterReload).toHaveAttribute(
      "aria-label",
      new RegExp(`bucket ${bucket.bucketName}`),
    );
  });
});
