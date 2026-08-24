import { test } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";

test("ListOperations detail", async ({ page }) => {
  page.on("request", (r) => {
    if (r.url().includes("ListOperations"))
      console.log(`>>> REQ ${r.url().split("/rpc/")[1]} :: ${r.postData()}`);
  });
  page.on("response", async (r) => {
    if (r.url().includes("ListOperations")) {
      let b = "";
      try {
        b = (await r.text()).slice(0, 200);
      } catch {
        /* stream */
      }
      console.log(`>>> RES ${r.status()} :: ${b}`);
    }
  });
  await loginAsAdmin(page);
  await page.goto("/tenants");
  await page.waitForTimeout(6_000);
});
