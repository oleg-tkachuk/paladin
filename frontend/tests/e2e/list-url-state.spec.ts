/**
 * A list keeps its view in the address bar: the search, the sort and the
 * toolbar's choice. A reload or a link opens the list as it was left.
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

test("a list opens on the choice a link names", async ({ page }) => {
  await loginAsAdmin(page);
  await gotoSettled(page, "/operations?filter=failed");
  await expect(page.getByRole("button", { name: "Failed" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await page.getByRole("button", { name: "All" }).click();
  await expect(page).not.toHaveURL(/filter=/);
});
