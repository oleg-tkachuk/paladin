/**
 * A list keeps its view in the address bar: the search, the sort and the
 * toolbar's facet choices. A reload or a link opens the list as it was left.
 *
 * The unit tests drive the hooks against jsdom's location. This drives the
 * real router, which answers a replace in a transition of its own: a search
 * box that waited for it would drop keystrokes, and a page that read the
 * router's view instead of the address bar would undo the last one.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { SEEDED_ADMIN } from "./fixtures/credentials";
import { gotoSettled } from "./fixtures/navigate";

test("a list opens with the search a link names and keeps what is typed", async ({
  page,
}) => {
  await loginAsAdmin(page);
  await gotoSettled(
    page,
    `/users?q=${encodeURIComponent(SEEDED_ADMIN.subject)}`,
  );
  const search = page.getByRole("searchbox", { name: "Search users" });
  await expect(search).toHaveValue(SEEDED_ADMIN.subject);
  await expect(
    page.getByRole("cell", { name: SEEDED_ADMIN.subject }).first(),
  ).toBeVisible();

  // Typed quickly, every keystroke lands, and the URL follows.
  await search.fill("");
  await search.pressSequentially("zz-no-such-user");
  await expect(search).toHaveValue("zz-no-such-user");
  await expect(page).toHaveURL(/[?&]q=zz-no-such-user(&|$)/);

  await page.getByRole("button", { name: /^User/ }).click();
  await expect(page).toHaveURL(/[?&]sort=user&dir=asc/);

  await page.reload();
  await expect(search).toHaveValue("zz-no-such-user");
  await expect(page).toHaveURL(/[?&]sort=user&dir=asc/);
});

test("a list opens on the facet choice a link names", async ({ page }) => {
  await loginAsAdmin(page);
  await gotoSettled(page, "/operations?state=FAILED");
  const state = page.getByRole("button", {
    name: "Filter by state, 1 chosen",
  });
  await expect(state).toBeVisible();
  await state.click();
  await page.getByRole("menuitemcheckbox", { name: /^Failed, / }).click();
  await expect(page).not.toHaveURL(/[?&]state=/);
});

/** The RPC that saves the user's console preferences. */
const UPDATE_MINE = "/UpdateMine";

// The choices are the signed-in user's preferences, shared by every spec
// that signs in as this admin: the test ends with them cleared, and clears
// them first in case a failed run left some behind.
test("a list remembers its facet choice until it is cleared", async ({
  page,
}) => {
  await loginAsAdmin(page);
  const saved = page.getByText("Saved filters");
  const clear = page.getByRole("button", { name: "Clear filters" });
  await gotoSettled(page, "/users");
  if (await saved.isVisible()) await clear.first().click();

  await page.getByRole("button", { name: "Filter by state" }).click();
  // Saved before the next visit reloads the page under the write.
  const written = page.waitForResponse((r) => r.url().endsWith(UPDATE_MINE));
  await page.getByRole("menuitemcheckbox", { name: /^Active, / }).click();
  await written;
  await page.keyboard.press("Escape");
  await expect(saved).toBeVisible();

  // A visit naming no facet opens on the saved choice, and writes it back.
  await gotoSettled(page, "/users");
  await expect(
    page.getByRole("button", { name: "Filter by state, 1 chosen" }),
  ).toBeVisible();
  await expect(page).toHaveURL(/[?&]state=active(&|$)/);

  // A link naming a facet wins over it.
  await gotoSettled(page, "/users?state=disabled");
  await expect(page).toHaveURL(/[?&]state=disabled(&|$)/);

  await clear.first().click();
  await expect(saved).toBeHidden();
  await gotoSettled(page, "/users");
  await expect(
    page.getByRole("button", { name: "Filter by state" }),
  ).toBeVisible();
  await expect(saved).toBeHidden();
});
