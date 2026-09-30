/**
 * Event subscriptions — the fan-out configuration.
 *
 * A subscription decides where a tenant's events are delivered, so a broken
 * one is silent by nature: nothing errors, events simply stop arriving
 * somewhere. The editor validates its CEL filter server-side before saving,
 * which is the same shape as the lifecycle rule editor and the same failure
 * mode — a form that looks fine carrying an expression the server rejects.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { seedSubscription, subscriptionCount } from "./fixtures/seed";
import { gotoSettled } from "./fixtures/navigate";

function subsURL(tenantId: string): string {
  return `/tenants/${tenantId}/event-subscriptions`;
}

/**
 * Open the subscription editor.
 *
 * The click is forced. The taller viewport fixed the other console forms, but
 * this page still fails to take a normal click when it runs after a
 * heavyweight file — the button is visible and enabled, and the click simply
 * times out. Forcing skips the actionability wait, not the visibility one, so
 * a genuinely missing button still fails; waiting for the dialog afterwards
 * keeps the failure honest if the press really did miss.
 */
async function openEditor(page: import("@playwright/test").Page) {
  const newSub = page.getByRole("button", { name: /New subscription/ }).first();
  await expect(newSub).toBeVisible({ timeout: 20_000 });

  // gotoSettled already waited for the shell to stop fetching; this second
  // wait covers a refetch triggered since then.
  await page
    .waitForLoadState("networkidle", { timeout: 30_000 })
    .catch(() => {});
  await newSub.click({ force: true });
  await expect(page.getByRole("dialog")).toBeVisible({ timeout: 15_000 });
  await expect(page.locator("#sub-http-url")).toBeVisible({ timeout: 15_000 });
}

test.describe("Event subscriptions", () => {
  test("a tenant with no subscriptions offers to create one", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();

    await gotoSettled(page, subsURL(tenant.tenantId));

    await expect(
      page.getByRole("button", { name: /New subscription/ }).first(),
    ).toBeEnabled({ timeout: 15_000 });
  });

  test("creating an HTTP subscription persists it", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    expect(await subscriptionCount(tenant.tenantId)).toBe(0);

    await gotoSettled(page, subsURL(tenant.tenantId));
    await openEditor(page);

    await page
      .locator("#sub-http-url")
      .fill("https://hooks.example.invalid/e2e");
    await page
      .getByRole("dialog")
      .getByRole("button", { name: /^Create subscription$/ })
      .click();

    await expect
      .poll(() => subscriptionCount(tenant.tenantId), { timeout: 15_000 })
      .toBe(1);
  });

  test("an invalid CEL filter is refused", async ({ page, makeTenant }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();

    await gotoSettled(page, subsURL(tenant.tenantId));
    await openEditor(page);

    await page
      .locator("#sub-http-url")
      .fill("https://hooks.example.invalid/e2e");
    // A field the EventEnvelope schema does not declare. Storing this would
    // produce a subscription whose filter never matches what its author meant.
    await page.locator("#sub-filter").fill('not_a_field == "x"');

    // The dialog validates the expression as it is typed and disables Create
    // outright — stronger than letting the server refuse it, because the
    // operator finds out before the round trip.
    //
    // The check is a round trip to CELService, so the refusal arrives
    // asynchronously and its latency tracks how busy the plane is. Inside the
    // full suite — twenty minutes of continuous traffic — 15s was not always
    // enough, and this test failed only there, never on its own. Poll with
    // headroom rather than asserting on one moment.
    const create = page
      .getByRole("dialog")
      .getByRole("button", { name: /^Create subscription$/ });
    await expect
      .poll(() => create.isDisabled(), { timeout: 30_000 })
      .toBe(true);

    // And the refusal has to be real, not just a greyed-out button.
    expect(await subscriptionCount(tenant.tenantId)).toBe(0);
  });

  test("an existing subscription is listed and can be deleted", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    await seedSubscription({ tenantId: tenant.tenantId });
    expect(await subscriptionCount(tenant.tenantId)).toBe(1);

    await gotoSettled(page, subsURL(tenant.tenantId));

    const row = page
      .getByRole("row")
      .filter({ hasText: "hooks.example.invalid" });
    await expect(row).toBeVisible({ timeout: 15_000 });

    // Each step waits for the one before it to actually appear. Chained
    // clicks read fine and fail under load: the menu has not mounted when the
    // second click fires, so it lands on nothing and the third never has a
    // dialog to confirm. This test failed only in the full suite, where the
    // browser is busiest, and passed every time it ran on its own.
    await row
      .getByRole("button", { name: /Actions|Delete/i })
      .first()
      .click({ force: true });

    // NOT forced, unlike the page-level buttons above. These two live in
    // overlays that animate in — a Radix dropdown and the confirm dialog — and
    // forcing clicks them mid-animation, when the transform can still put
    // their box outside the viewport. That is the failure this line produced:
    // "Element is outside of the viewport", on a menu item the screenshot
    // shows sitting comfortably on screen a moment later.
    //
    // Actionability is the right tool here: it waits for the element to stop
    // moving and to receive events, which is exactly the wait an animation
    // needs. Forcing is for something invisible overlapping the target, and
    // nothing overlaps these. The long timeout covers "slow under load", which
    // is the problem force was reached for.
    const deleteItem = page.getByRole("menuitem", { name: /Delete/i });
    await expect(deleteItem).toBeVisible({ timeout: 15_000 });
    await deleteItem.click({ timeout: 20_000 });

    const confirm = page.getByRole("button", { name: /^Delete/ }).last();
    await expect(confirm).toBeVisible({ timeout: 15_000 });
    await confirm.click({ timeout: 20_000 });

    await expect
      .poll(() => subscriptionCount(tenant.tenantId), { timeout: 15_000 })
      .toBe(0);
  });

  test("the filter hint's examples are valid expressions", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();

    await gotoSettled(page, subsURL(tenant.tenantId));
    await openEditor(page);

    // The hint is a worked example, so it has to work. It suggested
    // `event.kind == '...'` while the EventEnvelope schema declares bare
    // identifiers, so anyone copying it got "undeclared reference to 'event'"
    // — the same defect the lifecycle editor's placeholder had.
    const hint = await page
      .getByText(/Empty = all events\. Examples:/)
      .first()
      .textContent();
    expect(hint, "the filter field must suggest something").toBeTruthy();

    const first = /Examples:\s*([^,]+),/.exec(hint ?? "")?.[1]?.trim();
    expect(first, `could not read an example out of: ${hint}`).toBeTruthy();

    await page
      .locator("#sub-http-url")
      .fill("https://hooks.example.invalid/e2e");
    await page.locator("#sub-filter").fill(first as string);
    await page
      .getByRole("dialog")
      .getByRole("button", { name: /^Create subscription$/ })
      .click();

    await expect
      .poll(() => subscriptionCount(tenant.tenantId), { timeout: 30_000 })
      .toBe(1);
  });
});
