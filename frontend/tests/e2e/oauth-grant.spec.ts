/**
 * The OAuth grant, end to end (ADR-0035).
 *
 * Unit tests prove each piece; this proves the stack wires them: /oauth is
 * reachable where a client looks for it, the token carries the consent, the
 * planes enforce paladin.write, a rotation keeps the grant, and the console's
 * refresh token is not an OAuth one.
 *
 * Driven over HTTP rather than the consent page: the page only posts this
 * same form, and the 302 back to the client is read, not followed.
 */
import { test, expect, type APIRequestContext } from "@playwright/test";

import { SEEDED_ADMIN } from "./fixtures/credentials";
import {
  AUDIENCE_ADMIN,
  CLIENT_ID,
  HTTP_BAD_REQUEST,
  HTTP_OK,
  SCOPE_READ,
  SCOPE_WRITE,
  claims,
  grant,
  type TokenResponse,
} from "./fixtures/oauth";
import { ADMIN_URL, IAM_URL } from "./fixtures/seed";

// A read the admin plane serves to a platform admin, and a write whose empty
// request is refused by validation — after the scope check, so the two codes
// say which gate answered. Nothing is created either way.
const READ_RPC = "/paladin.admin.v1.BackendService/ListBackends";
const WRITE_RPC = "/paladin.admin.v1.CollectionService/CreateCollection";
const LOGIN_RPC = "/paladin.iam.v1.AuthService/Login";

/** The Connect error code of a unary JSON call, or "ok". */
async function call(
  request: APIRequestContext,
  rpc: string,
  token: string,
): Promise<string> {
  const res = await request.post(`${ADMIN_URL}${rpc}`, {
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    data: {},
  });
  if (res.ok()) return "ok";
  return ((await res.json()) as { code: string }).code;
}

test.describe("OAuth grant", () => {
  test("a read grant reads and is refused every write", async ({ request }) => {
    const tok = await grant(request, [SCOPE_READ]);
    expect(claims(tok.access_token)).toMatchObject({
      aud: AUDIENCE_ADMIN,
      client_id: CLIENT_ID,
      scope: SCOPE_READ,
    });
    expect(await call(request, READ_RPC, tok.access_token)).toBe("ok");
    expect(await call(request, WRITE_RPC, tok.access_token)).toBe(
      "permission_denied",
    );
  });

  test("a write grant passes the scope check", async ({ request }) => {
    const tok = await grant(request, [SCOPE_READ, SCOPE_WRITE]);
    // Past the scope check, the empty request meets validation.
    expect(await call(request, WRITE_RPC, tok.access_token)).toBe(
      "invalid_argument",
    );
  });

  test("a rotation keeps the grant's audience and scopes", async ({
    request,
  }) => {
    const first = await grant(request, [SCOPE_READ]);
    const res = await request.post(`${IAM_URL}/oauth/token`, {
      form: {
        grant_type: "refresh_token",
        client_id: CLIENT_ID,
        refresh_token: first.refresh_token,
      },
    });
    expect(res.status(), await res.text()).toBe(HTTP_OK);
    const rotated = (await res.json()) as TokenResponse;
    expect(claims(rotated.access_token)).toMatchObject({
      aud: AUDIENCE_ADMIN,
      client_id: CLIENT_ID,
      scope: SCOPE_READ,
    });
  });

  test("a console refresh token is not an OAuth one", async ({ request }) => {
    const login = await request.post(`${IAM_URL}${LOGIN_RPC}`, {
      headers: { "Content-Type": "application/json" },
      data: {
        subject: SEEDED_ADMIN.subject,
        password: SEEDED_ADMIN.password,
        requestedAudience: AUDIENCE_ADMIN,
      },
    });
    expect(login.status(), await login.text()).toBe(HTTP_OK);
    const { tokens } = (await login.json()) as {
      tokens: { refreshToken: string };
    };

    const res = await request.post(`${IAM_URL}/oauth/token`, {
      form: {
        grant_type: "refresh_token",
        client_id: CLIENT_ID,
        refresh_token: tokens.refreshToken,
      },
    });
    expect(res.status()).toBe(HTTP_BAD_REQUEST);
    expect(((await res.json()) as { error: string }).error).toBe(
      "invalid_grant",
    );
  });
});
