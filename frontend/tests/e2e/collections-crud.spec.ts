/**
 * Collections — creating, deleting, and the create dialog's binding requirement.
 *
 * The delete test was flaky for weeks, read as a test-timing problem. It was
 * the page: the delete looked the row's version up again at confirm, the search
 * typed a moment earlier had just changed the list's query, the list was empty
 * until it answered, and the delete went out with no version — a 400 the toast
 * reported as "must be empty". The page now keeps the row it was given.
 *
 * A Collection binds a tenant's namespace to a (backend, bucket) pair, so
 * creating one provisions storage and deleting one removes the binding every
 * object under it depends on. Delete is OCC-guarded; create is gated on
 * choosing a bucket, because a Collection with no binding has nowhere to put
 * bytes.
 *
 * buckets.spec.ts covers navigating to a Collection. This covers making and
 * removing them.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import {
  seedAdminTenantID,
  seedPhysicalBucket,
  collectionExists,
} from "./fixtures/seed";
import { gotoSettled } from "./fixtures/navigate";
import { uniqueSlug } from "./fixtures/unique";

function collectionsURL(tenantId: string): string {
  return `/tenants/${tenantId}/collections`;
}

/**
 * Open the create dialog.
 *
 * Forced: the button is visible and enabled by this point, and what remains
 * is Playwright's stability heuristic reacting to the shell still painting.
 * Forcing skips that wait, not the visibility one, so a genuinely missing or
 * covered button still fails.
 */
async function openCreateDialog(page: import("@playwright/test").Page) {
  const newBtn = page.getByRole("button", { name: /New Collection/ });
  await expect(newBtn).toBeVisible({ timeout: 20_000 });
  await newBtn.click({ force: true });
  await expect(page.getByRole("dialog")).toBeVisible({ timeout: 15_000 });
}

/** Open a Radix select and choose the option whose text matches. */
async function pickOption(
  page: import("@playwright/test").Page,
  trigger: import("@playwright/test").Locator,
  name: RegExp,
) {
  // Disabled until its list arrives (backends.length === 0), so waiting for
  // enabled is waiting for the data. Note Radix leaves a stale data-disabled
  // attribute on the trigger even once it is live — asserting on the
  // attribute instead waits forever.
  await expect(trigger).toBeEnabled({ timeout: 20_000 });
  await trigger.click({ force: true });
  const option = page.getByRole("option").filter({ hasText: name }).first();
  await expect(option).toBeVisible({ timeout: 10_000 });
  // Forced, unlike the dropdown menu item and the confirm dialog further down.
  // Unforcing this one was tried and reverted: `creating a Collection makes it
  // appear in the list` started failing on the poll for the collection to
  // exist, which is what a click that opens the list without selecting from it
  // looks like from the outside. The rule those two follow was written for
  // Radix dropdown items and dialogs; a Select option in this form is neither,
  // and the evidence for extending it here was a resemblance, not a finding.
  await option.click({ force: true });
  // Wait for the list to close rather than for the trigger's text: the label
  // is composed ("<id> — <display name>") and which option the list offers
  // first is not this test's business. What matters is that a choice landed,
  // because the dependent select only repopulates after it does.
  await expect(page.getByRole("option").first()).toBeHidden({
    timeout: 10_000,
  });
}

test.describe("Collections CRUD", () => {
  test("the create dialog refuses to submit without a bucket", async ({
    page,
    makeEnabledBackend,
    trackCollection,
  }) => {
    // A backend with no buckets is the only way to reach the no-bucket state
    // now: the dialog defaults to a backend that HAS buckets, precisely so an
    // operator is not dropped into a form that cannot be completed. This test
    // used to pass because that default was broken and picked an empty
    // backend every time — it asserted the guard while demonstrating the bug.
    const empty = await makeEnabledBackend();
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();

    await gotoSettled(page, collectionsURL(tenantId));
    await openCreateDialog(page);

    const dialog = page.getByRole("dialog");
    await expect(dialog.getByLabel(/^Path/)).toBeVisible({ timeout: 15_000 });
    const created = `e2e/${uniqueSlug("ok")}`;
    trackCollection({
      tenantId,
      bucket: "",
      collection: created,
      displayName: "",
    });
    await dialog.getByLabel(/^Path/).fill(created);

    // Choosing the empty backend leaves nothing to bind to.
    await pickOption(
      page,
      dialog.getByRole("combobox", { name: /^Backend/ }),
      new RegExp(empty.backendId),
    );

    // A name alone is not enough: without a bucket the Collection has no
    // storage to bind to, so the dialog holds the submit rather than creating
    // something unusable — and says why rather than leaving the operator to
    // guess which field is at fault.
    await expect(
      dialog.getByRole("combobox", { name: /^Bucket/ }),
    ).toBeDisabled();
    await expect(
      dialog.getByRole("button", { name: /^Create Collection$/ }),
    ).toBeDisabled();
  });

  test("creating a Collection makes it appear in the list", async ({
    page,
    trackCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    await seedPhysicalBucket();

    await gotoSettled(page, collectionsURL(tenantId));
    await openCreateDialog(page);

    const dialog = page.getByRole("dialog");
    const name = `e2e/${uniqueSlug("ok")}`;
    trackCollection({
      tenantId,
      bucket: "",
      collection: name,
      displayName: "",
    });
    await dialog.getByLabel(/^Path/).fill(name);

    // Backend then bucket: the bucket list is derived from the chosen backend,
    // so picking them out of order leaves the second empty.
    await pickOption(
      page,
      dialog.getByRole("combobox", { name: /^Backend/ }),
      /^primary$/,
    );
    await pickOption(
      page,
      dialog.getByRole("combobox", { name: /^Bucket/ }),
      /./,
    );

    const submit = dialog.getByRole("button", { name: /^Create Collection$/ });
    await expect(submit).toBeEnabled();
    await submit.click({ force: true });

    await expect
      .poll(() => collectionExists(tenantId, name), { timeout: 20_000 })
      .toBe(true);

    // Narrow the list before looking for the row — filtering is what an
    // operator does, and it keeps the assertion off a long table.
    //
    // The filter is server-side and debounced now, so the assertion below has
    // to outlast 300ms plus a round trip; its 15s timeout does. The locator
    // used to name the old placeholder, "Search Collections by prefix", and
    // both tests in this file failed the moment that text changed — which is
    // the coupling working, not a surprise: a placeholder is user-visible
    // copy, and a test that spells it out is asserting the copy.
    await page.getByPlaceholder(/search by name/i).fill(name);
    await expect(page.getByText(name).first()).toBeVisible({ timeout: 15_000 });
  });

  test("deleting a Collection removes it", async ({ page, makeCollection }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedPhysicalBucket();
    const collection = await makeCollection({ tenantId, bucket });

    await gotoSettled(page, collectionsURL(tenantId));
    await page.getByPlaceholder(/search by name/i).fill(collection.collection);

    const row = page
      .getByRole("row")
      .filter({ hasText: collection.collection });
    await expect(row).toBeVisible({ timeout: 15_000 });

    await row.getByRole("button", { name: /Actions/i }).click({ force: true });

    // NOT forced, unlike the row trigger above — see the same sequence in
    // event-subscriptions.spec.ts, where this exact failure was diagnosed and
    // fixed. The menu item and the confirm below live in overlays that animate
    // in, and forcing clicks them mid-transform, when their box can still be
    // outside the viewport. Actionability is the wait an animation needs; the
    // long timeout covers "slow under load", which is what force was reached
    // for. This test failed on CI runs whose commits touched no application
    // code at all, three times in ten runs.
    const deleteItem = page.getByRole("menuitem", { name: /Delete/i });
    await expect(deleteItem).toBeVisible({ timeout: 15_000 });
    await deleteItem.click({ timeout: 20_000 });

    // The confirm dialog names what is about to go, because a Collection is
    // the binding every object under it resolves through.
    await expect(page.getByText(/Delete this Collection\?/i)).toBeVisible();
    const confirm = page.getByRole("button", { name: /^Delete Collection$/ });
    await expect(confirm).toBeVisible({ timeout: 15_000 });
    await confirm.click({ timeout: 20_000 });

    await expect
      .poll(() => collectionExists(tenantId, collection.collection), {
        timeout: 20_000,
      })
      .toBe(false);
  });
});
