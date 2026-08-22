/**
 * US5 — Tenant restore from trash.
 *
 * Spec: specs/001-frontend-playwright-e2e/spec.md §"User Story 5".
 * Two scenarios (the original third "slug collision on restore"
 * was dropped during /speckit-analyze as unreachable in the
 * current backend — see spec.md §US5).
 *
 * Per-test seed: one tenant created via API. The delete + restore
 * flows are driven through the UI to exercise the dialog + button
 * affordances.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { seedTenant } from "./fixtures/seed";

test.describe("US5 — Tenant restore from trash", () => {
  test("soft-delete moves tenant from /tenants to /trash", async ({ page }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();

    await page.goto("/tenants");
    await expect(page.getByText(tenant.slug)).toBeVisible({
      timeout: 5_000,
    });

    // Row dropdown: trigger button carries aria-label
    // "Actions for <tenantId>" — unique per row.
    const actions = page.getByRole("button", {
      name: `Actions for ${tenant.tenantId}`,
    });
    await actions.click();
    await page.getByRole("menuitem", { name: /Delete tenant/ }).click();

    // Confirm in the AlertDialog. There are two matching
    // strings: the menuitem AND the dialog's confirm button.
    // After the dialog opens we want the confirm button —
    // matched by role=button (the menuitem already closed).
    await page.getByRole("button", { name: /^Delete tenant$/ }).click();

    // Confirm the delete actually happened before asserting on its effects.
    // The dialog deletes with OCC — deleteTenant(id, resourceVersion) — so a
    // stale version is refused and the UI says "Deletion failed". Without
    // this the test walks past that and fails somewhere downstream, blaming
    // whichever assertion it reached first.
    await expect(page.getByText("Tenant deleted")).toBeVisible({
      timeout: 10_000,
    });

    // After delete: the tenant is gone from /tenants. We
    // reload to bypass any optimistic-UI cache and assert the
    // server-truth list excludes the row.
    await page.reload();
    await expect(page.getByText(tenant.slug)).not.toBeVisible({
      timeout: 5_000,
    });

    // And the tenant appears in /trash.
    await page.goto("/trash");
    await expect(page.getByText(tenant.slug)).toBeVisible({
      timeout: 5_000,
    });
  });

  test("restore from /trash returns the tenant to /tenants", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();

    // Drive through the full lifecycle: delete via UI, then
    // restore via UI. We could shortcut the delete via API
    // (would shave ~500ms) but having the same operator path
    // catches any regression in either direction.
    await page.goto("/tenants");

    // Wait for the row before opening its menu. The sibling test above does
    // this and passes; this one went straight for the Actions button, and
    // the list re-rendering underneath the click is enough to lose it — the
    // database shows tenants from failed runs created and never deleted,
    // which is what a dropped click looks like from the outside.
    await expect(page.getByText(tenant.slug)).toBeVisible({ timeout: 10_000 });

    const actions = page.getByRole("button", {
      name: `Actions for ${tenant.tenantId}`,
    });
    await actions.click();
    await page.getByRole("menuitem", { name: /Delete tenant/ }).click();
    await page.getByRole("button", { name: /^Delete tenant$/ }).click();

    // Same OCC confirmation as the sibling test: prove the delete landed
    // before going looking for its result in the trash.
    await expect(page.getByText("Tenant deleted")).toBeVisible({
      timeout: 10_000,
    });

    // Restore from /trash.
    await page.goto("/trash");

    // Wait for the row before reaching into it. The delete above is a
    // round-trip the test does not await: it clicked confirm and navigated,
    // so arriving here does not mean the tenant is in the trash list yet.
    // Going straight for the button made the failure a 20s click timeout on
    // a locator that never resolved — which reads as "Restore is broken"
    // rather than "the row had not arrived". The sibling test above already
    // waits like this after its own delete.
    await expect(page.getByText(tenant.slug)).toBeVisible({ timeout: 10_000 });

    // The Restore button per row — text is the affordance
    // ("Restore" / "Restoring…" mid-flight per page.tsx:228).
    const restoreRow = page
      .getByRole("row", { name: new RegExp(tenant.slug) })
      .getByRole("button", { name: /^Restore$/ });
    await restoreRow.click();

    // Reload /tenants and verify the tenant is back. The
    // restore flow flips deleted_at = NULL; the list should
    // include the row again on the next ListTenants call.
    await page.goto("/tenants");
    await expect(page.getByText(tenant.slug)).toBeVisible({
      timeout: 5_000,
    });
    // And it's GONE from /trash.
    await page.goto("/trash");
    await expect(page.getByText(tenant.slug)).not.toBeVisible({
      timeout: 5_000,
    });
  });
});
