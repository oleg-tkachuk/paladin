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
import { ADMIN_URL, IAM_URL, seedAdminTenantID } from "./fixtures/seed";

// The client backend/configs/compose.yaml seeds (and the dev cluster's values).
const CLIENT_ID = "paladin-e2e-agent";
const REDIRECT_URI = "https://e2e-agent.invalid/callback";
const SCOPE_READ = "paladin.read";
const SCOPE_WRITE = "paladin.write";
const AUDIENCE_ADMIN = "paladin-admin";

// RFC 7636 allows 43–128 characters; the verifier only has to match itself.
const PKCE_VERIFIER = "e2e-verifier-".padEnd(64, "x");

// A read the admin plane serves to a platform admin, and a write whose empty
// request is refused by validation — after the scope check, so the two codes
// say which gate answered. Nothing is created either way.
const READ_RPC = "/paladin.admin.v1.BackendService/ListBackends";
const WRITE_RPC = "/paladin.admin.v1.CollectionService/CreateCollection";
const LOGIN_RPC = "/paladin.iam.v1.AuthService/Login";

const HTTP_FOUND = 302;
const HTTP_OK = 200;
const HTTP_BAD_REQUEST = 400;

interface TokenResponse {
  access_token: string;
  refresh_token: string;
  scope?: string;
}

async function pkceChallenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(verifier),
  );
  return Buffer.from(digest).toString("base64url");
}

function claims(token: string): Record<string, unknown> {
  return JSON.parse(
    Buffer.from(token.split(".")[1], "base64url").toString("utf8"),
  );
}

/** Consent as the seeded admin and exchange the code. */
async function grant(
  request: APIRequestContext,
  scopes: string[],
): Promise<TokenResponse> {
  const authorize = await request.post(`${IAM_URL}/oauth/authorize`, {
    form: {
      client_id: CLIENT_ID,
      redirect_uri: REDIRECT_URI,
      scope: scopes.join(" "),
      resource: AUDIENCE_ADMIN,
      code_challenge: await pkceChallenge(PKCE_VERIFIER),
      code_challenge_method: "S256",
      state: "e2e",
      action: "allow",
      username: SEEDED_ADMIN.subject,
      password: SEEDED_ADMIN.password,
      tenant: await seedAdminTenantID(),
    },
    maxRedirects: 0,
  });
  expect(authorize.status(), await authorize.text()).toBe(HTTP_FOUND);
  const code = new URL(authorize.headers()["location"]).searchParams.get(
    "code",
  );
  expect(code, "the redirect carries a code").toBeTruthy();

  const token = await request.post(`${IAM_URL}/oauth/token`, {
    form: {
      grant_type: "authorization_code",
      code: code as string,
      client_id: CLIENT_ID,
      redirect_uri: REDIRECT_URI,
      code_verifier: PKCE_VERIFIER,
    },
  });
  expect(token.status(), await token.text()).toBe(HTTP_OK);
  return (await token.json()) as TokenResponse;
}

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
