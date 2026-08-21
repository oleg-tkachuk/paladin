/**
 * US2 — Tenant + backend + bucket scope switching.
 *
 * Spec: specs/001-frontend-playwright-e2e/spec.md §"User Story 2".
 *
 * The original "tenant scope switching" wording is BACK: the backend now
 * ships ListMyMemberships + SwitchTenant (#99) and the ScopePicker's
 * tenant row is a real switcher (#100). The backend/bucket scenarios that
 * carried US2 while switching didn't exist stay as a regression guard for
 * the picker plumbing.
 *
 * Scenarios:
 *   1. picker opens; sections render with current state
 *   2. selecting a bucket flips the topbar breadcrumb label
 *   3. reload preserves the localStorage'd scope
 *   4. switching to a second tenant re-scopes the session — a real
 *      SwitchTenant round-trip: the fixture seeds a membership (a users
 *      row for the same subject in a fresh tenant), the picker switches
 *      into it, and the topbar re-labels
 *
 * Per-test fixture: one seeded bucket under the default
 * `primary` backend. The picker needs at least one selectable
 * row for the swap test to be meaningful.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { seedBucket, seedTenantMembership } from "./fixtures/seed";

// The ScopePicker is a two-level Radix popover (ScopePicker.tsx): the main
// trigger ("Scope picker — …") opens a popover holding two ScopeRow buttons —
// "Switch backend — …" and "Switch bucket — …". Each ScopeRow opens its OWN
// nested cmdk popover (CommandGroup heading "Backends"/"Buckets") with the
// selectable options. So bucket options aren't reachable from the main popover
// directly — you open the bucket sub-row first.
async function selectBucket(
  page: import("@playwright/test").Page,
  bucketId: string,
) {
  await page.getByRole("button", { name: /^Scope picker —/ }).click();
  await page.getByRole("button", { name: /^Switch bucket/ }).click();
  // Filter the cmdk list down to the seeded bucket — the list accumulates
  // buckets across runs, so searching keeps the option reliably rendered.
  await page.getByPlaceholder("Search buckets…").fill(bucketId);
  await page
    .getByRole("option", { name: new RegExp(bucketId) })
    .first()
    .click();
}

test.describe("US2 — Tenant + backend + bucket scope switching", () => {
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

    await selectBucket(page, bucket.bucketId);

    // Selecting closes the nested row popover; the main trigger's
    // aria-label now embeds the chosen bucket (ScopePicker.tsx:130 —
    // "… bucket <bucketId>, tenant <tenantLabel>").
    await expect(
      page.getByRole("button", { name: /^Scope picker —/ }),
    ).toHaveAttribute("aria-label", new RegExp(`bucket ${bucket.bucketId}`));
  });

  test("reload preserves the selected scope", async ({ page }) => {
    const bucket = await seedBucket();
    await loginAsAdmin(page);

    await selectBucket(page, bucket.bucketId);

    // Pre-reload sanity: scope is set.
    await expect(
      page.getByRole("button", { name: /^Scope picker —/ }),
    ).toHaveAttribute("aria-label", new RegExp(`bucket ${bucket.bucketId}`));

    // The localStorage write happens synchronously in
    // setBucket / setScope; reload must round-trip it.
    await page.reload();

    // Same locator after reload — re-resolves the topbar
    // post-hydration. If safeRead() in ScopeContext skipped
    // the localStorage key, the aria-label would fall back
    // to "bucket any" and the assertion fails.
    await expect(
      page.getByRole("button", { name: /^Scope picker —/ }),
    ).toHaveAttribute("aria-label", new RegExp(`bucket ${bucket.bucketId}`));
  });

  test("switching to a second tenant re-scopes the session", async ({
    page,
  }) => {
    // Seed BEFORE login: a fresh tenant + a users row for the SAME subject
    // inside it (per-membership roles: tenant.admin there, not
    // platform.admin). SwitchTenant needs no password in the target —
    // the switch re-mints off the caller's existing identity.
    const membership = await seedTenantMembership();
    await loginAsAdmin(page);

    // Open the picker, then the tenant ScopeRow. Memberships load lazily
    // when the picker opens (useMemberships), so the row's cmdk list may
    // briefly show only the signed-in tenant — the search below waits for
    // the target option to render.
    await page.getByRole("button", { name: /^Scope picker —/ }).click();
    await page.getByRole("button", { name: /^Switch tenant/ }).click();
    await page.getByPlaceholder("Search tenants…").fill(membership.slug);
    await page
      .getByRole("option", { name: new RegExp(membership.slug) })
      .first()
      .click();

    // The switch swaps the token cache + user record (AuthContext), which
    // re-labels the topbar trigger with the TARGET tenant. Assert on the
    // tenant-id prefix, not displayName: the label falls back to
    // `<uuid8>…` until GetTenant resolves the record, and for a fresh
    // tenant.admin membership that lookup may be Cedar-denied — the id
    // prefix is the invariant that proves the session re-scoped.
    await expect(
      page.getByRole("button", { name: /^Scope picker —/ }),
    ).toHaveAttribute(
      "aria-label",
      new RegExp(`tenant .*${membership.tenantId.slice(0, 8)}`),
      { timeout: 10_000 },
    );

    // The soft scope reset on tenant change: backend/bucket fall back to
    // "any" — the previous tenant's coordinates must not leak across.
    await expect(
      page.getByRole("button", { name: /^Scope picker —/ }),
    ).toHaveAttribute("aria-label", /backend any, bucket any/);
  });
});
