/**
 * The top bar's tenant switcher.
 *
 * It names the tenant the session is signed in to, lists the subject's
 * memberships, and switching to one re-scopes the session through a real
 * SwitchTenant round-trip: the fixture seeds a membership (a users row for
 * the same subject in a fresh tenant), the switcher switches into it, and
 * the trigger re-labels.
 *
 * It replaced a scope picker that also held a backend and a bucket nobody
 * read; the first test pins that they are gone.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";

// The trigger's accessible name: "Tenant switcher — signed in to <tenant>".
const TRIGGER = /^Tenant switcher — signed in to /;

test.describe("Tenant switcher", () => {
  test("names the signed-in tenant and lists it as current", async ({
    page,
  }) => {
    await loginAsAdmin(page);

    const trigger = page.getByRole("button", { name: TRIGGER });
    await expect(trigger).toBeVisible();
    await trigger.click();

    await expect(page.getByPlaceholder("Search tenants…")).toBeVisible();
    await expect(page.getByLabel("Signed in")).toBeVisible();
    await expect(page.getByText(/^Backend$|^Bucket$/i)).toHaveCount(0);
  });

  test("switching to a second tenant re-scopes the session", async ({
    page,
    makeMembership,
  }) => {
    // Seed BEFORE login: a fresh tenant + a users row for the SAME subject
    // inside it (per-membership roles: tenant.admin there, not
    // platform.admin). SwitchTenant needs no password in the target —
    // the switch re-mints off the caller's existing identity.
    const membership = await makeMembership();
    await loginAsAdmin(page);

    // Memberships load when the switcher opens, so the list may briefly hold
    // only the signed-in tenant — the search waits for the target to render.
    await page.getByRole("button", { name: TRIGGER }).click();
    await page.getByPlaceholder("Search tenants…").fill(membership.slug);
    await page
      .getByRole("option", { name: new RegExp(membership.slug) })
      .first()
      .click();

    // The label falls back to `<uuid8>…` until GetTenant resolves the
    // record, then becomes the display name. Which one the assertion sees
    // is a race; either proves the session re-scoped.
    const idPrefix = membership.tenantId.slice(0, 8);
    await expect(page.getByRole("button", { name: TRIGGER })).toHaveAttribute(
      "aria-label",
      new RegExp(
        `signed in to (${idPrefix}|${escapeRegExp(membership.displayName)})`,
      ),
      { timeout: 10_000 },
    );
  });
});

// Display names carry generated suffixes and are interpolated into a pattern,
// so they have to be escaped — an unescaped one would silently turn into a
// pattern that matches something else.
function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
