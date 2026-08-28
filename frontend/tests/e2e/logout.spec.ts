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
 * a UI detail that could be true while the session lives on.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";

const COOKIE = "paladin_rt_iam";

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
    //
    // This call also rotates the chain, which is what makes the replay below
    // the sharper test: the value we hold afterwards is the PREDECESSOR of the
    // head that logout revokes. A logout that killed only the token it was
    // handed left this one alive, and the supersession window honoured it for
    // another thirty seconds — a logged-out session that still worked.
    const alive = await request.get(`${baseURL}/api/auth/me`, {
      headers: { Cookie: `${COOKIE}=${before!.value}` },
    });
    expect(alive.status(), "captured cookie was not a live session").toBe(200);

    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(page).toHaveURL(/\/login/, { timeout: 10_000 });

    // The browser forgot it.
    const after = (await page.context().cookies()).find(
      (c) => c.name === COOKIE,
    );
    expect(after?.value ?? "", "cookie still set in the browser").toBe("");

    // And — the part that was broken — so did the server. This request carries
    // the pre-logout value directly, which is exactly what an attacker holding
    // a copied cookie would send.
    const replayed = await request.get(`${baseURL}/api/auth/me`, {
      headers: { Cookie: `${COOKIE}=${before!.value}` },
    });
    expect(
      replayed.status(),
      "the refresh chain outlived the logout that was supposed to revoke it",
    ).toBe(401);
  });

  test("reports whether the server-side revocation actually happened", async ({
    page,
    request,
    baseURL,
  }) => {
    // ok:true alone claimed a session had ended when it had not. The response
    // carries the outcome now, so a client can tell a local logout from a
    // complete one.
    await loginAsAdmin(page);
    const cookie = (await page.context().cookies()).find(
      (c) => c.name === COOKIE,
    );

    const res = await request.post(`${baseURL}/api/auth/logout`, {
      headers: { Cookie: `${COOKIE}=${cookie!.value}` },
    });
    expect(res.status()).toBe(200);
    expect(await res.json()).toMatchObject({ ok: true, revoked: true });
  });
});
