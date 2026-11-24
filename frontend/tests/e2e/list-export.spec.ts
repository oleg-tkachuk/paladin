/**
 * A list's toolbar exports what the list shows as CSV: the rows its search
 * and facets leave, under the headers its table shows. The unit tests pin
 * the text; this drives the browser's own download of it.
 */
import { readFile } from "node:fs/promises";

import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { SEEDED_ADMIN } from "./fixtures/credentials";
import { gotoSettled } from "./fixtures/navigate";

/** The file name an export of the users list takes. */
const USERS_EXPORT = /^users-\d{8}-\d{6}\.csv$/;
/** The byte order mark the file opens with. */
const BOM = "﻿";

test("a list exports the rows its search leaves, as CSV", async ({ page }) => {
  await loginAsAdmin(page);
  await gotoSettled(
    page,
    `/users?q=${encodeURIComponent(SEEDED_ADMIN.subject)}`,
  );
  await expect(
    page.getByRole("cell", { name: SEEDED_ADMIN.subject }).first(),
  ).toBeVisible();

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Export as CSV" }).click(),
  ]);
  expect(download.suggestedFilename()).toMatch(USERS_EXPORT);
  const path = await download.path();
  const text = await readFile(path, "utf8");
  const [header, ...rows] = text.replace(BOM, "").trimEnd().split("\r\n");
  expect(header).toBe("User,Tenant,Roles,Last login,State");
  expect(rows.length).toBeGreaterThan(0);
  for (const row of rows) expect(row).toContain(SEEDED_ADMIN.subject);
});
