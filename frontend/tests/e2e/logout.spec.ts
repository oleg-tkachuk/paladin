/**
 * Logout, asserted where it actually has to be true: on the server.
 *
 * There was no test here at all, and that is precisely why logout spent its
 * life revoking nothing. The IAM plane requires an Authorization header on
 * Revoke; the BFF sent none; the resulting error was caught, logged and
 * dropped; the cookie was cleared and the UI showed a logged-out user while
 * the refresh chain stayed live for its full seven days. Anyone still holding
 * that cookie value kept a working session the user believed they had ended.
 *
 * The route's unit test passed throughout, because it mocked Revoke as
 * succeeding and asserted only that it was called. A mock cannot refuse the
 * way a server refuses.
 *
 * So the load-bearing assertion below is not that the browser reached /login —
 * it is that the captured cookie value is dead afterwards. Everything else is
 * a UI detail that could be true while the session lives on. The session is
 * the session role's now (ADR-0048); the cookie is the whole credential, so
 * replaying it is exactly what an attacker holding a copy would do.
 */
import { test, expect, type APIRequestContext } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";

const COOKIE = "__Host-paladin_session";
const WHO_AM_I = "/session/rpc/paladin.iam.v1.AuthService/WhoAmI";
const HTTP_NO_CONTENT = 204;
const HTTP_UNAUTHORIZED = 401;

// whoAmI asks the session role who the cookie value belongs to, from the
// console's origin, as the page does.
function whoAmI(request: APIRequestContext, baseURL: string, cookie: string) {
  return request.post(`${baseURL}${WHO_AM_I}`, {
    headers: {
      Cookie: `${COOKIE}=${cookie}`,
      Origin: baseURL,
      "Content-Type": "application/json",
    },
    data: {},
  });
}

test.describe("Logout", () => {
  test("ends the session on the server, not just in the browser", async ({
    page,
    request,
    baseURL,
  }) => {
    await loginAsAdmin(page);

    const before = (await page.context().cookies()).find(
      (c) => c.name === COOKIE,
    );
    expect(before?.value, "no session cookie after login").toBeTruthy();

    // Prove the captured value is genuinely a working session BEFORE logout,
    // or the assertion after it would pass against a token that never worked.
    const alive = await whoAmI(request, baseURL!, before!.value);
    expect(alive.status(), "captured cookie was not a live session").toBe(200);

    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page).toHaveURL(/\/login/, { timeout: 10_000 });

    // The browser forgot it.
    const after = (await page.context().cookies()).find(
      (c) => c.name === COOKIE,
    );
    expect(after?.value ?? "", "cookie still set in the browser").toBe("");

    // And — the part that was broken — so did the server.
    const replayed = await whoAmI(request, baseURL!, before!.value);
    expect(
      replayed.status(),
      "the session outlived the logout that was supposed to revoke it",
    ).toBe(HTTP_UNAUTHORIZED);
  });

  test("revokes the session it is called with", async ({
    page,
    request,
    baseURL,
  }) => {
    await loginAsAdmin(page);
    const cookie = (await page.context().cookies()).find(
      (c) => c.name === COOKIE,
    );

    const res = await request.post(`${baseURL}/session/logout`, {
      headers: { Cookie: `${COOKIE}=${cookie!.value}`, Origin: baseURL! },
    });
    expect(res.status()).toBe(HTTP_NO_CONTENT);
    expect((await whoAmI(request, baseURL!, cookie!.value)).status()).toBe(
      HTTP_UNAUTHORIZED,
    );
  });
});
